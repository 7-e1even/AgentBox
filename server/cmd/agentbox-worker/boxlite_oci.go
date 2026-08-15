package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const workerImageMetadataName = ".agentbox-image.json"

type workerImageMetadata struct {
	ID           string   `json:"id"`
	References   []string `json:"references"`
	Architecture string   `json:"architecture"`
	Size         string   `json:"size"`
	Created      string   `json:"created"`
	Format       string   `json:"format"`
	Path         string   `json:"path"`
	Source       string   `json:"source"`
}

func runImageToOCI(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("image-to-oci", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	archivePath := flags.String("archive", "", "docker save archive")
	outputPath := flags.String("output", "", "OCI layout output directory")
	reference := flags.String("reference", "", "source image reference")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("usage: agentbox-worker image-to-oci --output <directory> --reference <image> [--archive <docker-save.tar>]: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*outputPath) == "" || strings.TrimSpace(*reference) == "" {
		return errors.New("usage: agentbox-worker image-to-oci --output <directory> --reference <image> [--archive <docker-save.tar>]")
	}

	output := filepath.Clean(*outputPath)
	if validOCIImageLayout(output) {
		if err := registerWorkerImageReference(output, strings.TrimSpace(*reference)); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, output)
		return err
	}
	if strings.TrimSpace(*archivePath) == "" {
		return fmt.Errorf("docker archive is required because %s is not a valid OCI image layout", output)
	}
	if err := materializeWorkerOCI(*archivePath, output, strings.TrimSpace(*reference)); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, output)
	return err
}

func materializeWorkerOCI(archivePath, outputPath, reference string) error {
	image, err := tarball.ImageFromPath(archivePath, nil)
	if err != nil {
		return fmt.Errorf("read Docker image archive: %w", err)
	}
	image = mutate.MediaType(image, types.OCIManifestSchema1)
	image = mutate.ConfigMediaType(image, types.OCIConfigJSON)

	config, err := image.ConfigFile()
	if err != nil {
		return fmt.Errorf("read Docker image configuration: %w", err)
	}
	configDigest, err := image.ConfigName()
	if err != nil {
		return fmt.Errorf("read Docker image ID: %w", err)
	}
	size, err := ociImageContentSize(image)
	if err != nil {
		return fmt.Errorf("calculate OCI image content size: %w", err)
	}

	index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{
		Add: image,
		Descriptor: v1.Descriptor{
			MediaType: types.OCIManifestSchema1,
			Platform: &v1.Platform{
				Architecture: config.Architecture,
				OS:           config.OS,
			},
			Annotations: map[string]string{
				"org.opencontainers.image.ref.name": reference,
			},
		},
	})
	index = mutate.IndexMediaType(index, types.OCIImageIndex)

	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create Worker OCI image directory: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".worker-oci-")
	if err != nil {
		return fmt.Errorf("create temporary OCI layout: %w", err)
	}
	defer os.RemoveAll(temporary)

	if _, err := layout.Write(temporary, index); err != nil {
		return fmt.Errorf("write OCI image layout: %w", err)
	}
	created := ""
	if !config.Created.IsZero() {
		created = config.Created.UTC().Format(time.RFC3339)
	}
	metadata := workerImageMetadata{
		ID:           configDigest.String(),
		References:   []string{reference},
		Architecture: config.Architecture,
		Size:         formatByteSize(size),
		Created:      created,
		Format:       "oci",
		Path:         outputPath,
		Source:       "worker-oci",
	}
	if err := writeWorkerImageMetadata(temporary, metadata); err != nil {
		return err
	}
	if err := os.Rename(temporary, outputPath); err != nil {
		if validOCIImageLayout(outputPath) {
			return registerWorkerImageReference(outputPath, reference)
		}
		return fmt.Errorf("publish OCI image layout: %w", err)
	}
	return nil
}

func ociImageContentSize(image v1.Image) (int64, error) {
	manifest, err := image.RawManifest()
	if err != nil {
		return 0, err
	}
	config, err := image.RawConfigFile()
	if err != nil {
		return 0, err
	}
	total := int64(len(manifest) + len(config))
	layers, err := image.Layers()
	if err != nil {
		return 0, err
	}
	for _, layer := range layers {
		size, err := layer.Size()
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total, nil
}

func validOCIImageLayout(path string) bool {
	index, err := layout.ImageIndexFromPath(path)
	if err != nil {
		return false
	}
	manifest, err := index.IndexManifest()
	return err == nil && len(manifest.Manifests) > 0
}

func registerWorkerImageReference(path, reference string) error {
	metadataPath := filepath.Join(path, workerImageMetadataName)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return fmt.Errorf("read Worker OCI image metadata: %w", err)
	}
	var metadata workerImageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("parse Worker OCI image metadata: %w", err)
	}
	for _, existing := range metadata.References {
		if existing == reference {
			return nil
		}
	}
	metadata.References = append(metadata.References, reference)
	sort.Strings(metadata.References)
	return writeWorkerImageMetadata(path, metadata)
}

func writeWorkerImageMetadata(layoutPath string, metadata workerImageMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode Worker OCI image metadata: %w", err)
	}
	temporary, err := os.CreateTemp(layoutPath, ".agentbox-image-")
	if err != nil {
		return fmt.Errorf("create Worker OCI image metadata: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure Worker OCI image metadata: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write Worker OCI image metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Worker OCI image metadata: %w", err)
	}
	targetPath := filepath.Join(layoutPath, workerImageMetadataName)
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		if removeErr := os.Remove(targetPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("replace Worker OCI image metadata: %w", removeErr)
		}
		if retryErr := os.Rename(temporaryPath, targetPath); retryErr != nil {
			return fmt.Errorf("publish Worker OCI image metadata: %w", retryErr)
		}
	}
	return nil
}

func formatByteSize(bytes int64) string {
	if bytes < 0 {
		return ""
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		if value < 1024 || suffix == "TiB" {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return ""
}
