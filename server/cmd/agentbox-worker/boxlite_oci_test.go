package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func TestRunImageToOCIMaterializesDockerArchiveAndRegistersReferences(t *testing.T) {
	temporary := t.TempDir()
	archivePath := filepath.Join(temporary, "image.tar")
	outputPath := filepath.Join(temporary, "layouts", "image-id")
	reference := "example.test/agentbox:latest"

	config, err := empty.Image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config = config.DeepCopy()
	config.Architecture = "amd64"
	config.OS = "linux"
	image, err := mutate.ConfigFile(empty.Image, config)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := tarball.WriteToFile(archivePath, tag, image); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runImageToOCI([]string{
		"--archive", archivePath,
		"--output", outputPath,
		"--reference", reference,
	}, &stdout); err != nil {
		t.Fatalf("runImageToOCI() error = %v", err)
	}
	if got := stdout.String(); got != outputPath+"\n" {
		t.Fatalf("runImageToOCI() output = %q, want %q", got, outputPath+"\n")
	}

	index, err := layout.ImageIndexFromPath(outputPath)
	if err != nil {
		t.Fatalf("read generated OCI layout: %v", err)
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		t.Fatalf("read generated OCI index: %v", err)
	}
	if manifest.MediaType != types.OCIImageIndex {
		t.Fatalf("index media type = %q, want %q", manifest.MediaType, types.OCIImageIndex)
	}
	if len(manifest.Manifests) != 1 || manifest.Manifests[0].MediaType != types.OCIManifestSchema1 {
		t.Fatalf("generated manifest descriptors = %#v", manifest.Manifests)
	}
	if manifest.Manifests[0].Platform == nil || manifest.Manifests[0].Platform.Architecture != "amd64" {
		t.Fatalf("generated manifest platform = %#v", manifest.Manifests[0].Platform)
	}

	secondReference := "example.test/agentbox:stable"
	stdout.Reset()
	if err := runImageToOCI([]string{
		"--output", outputPath,
		"--reference", secondReference,
	}, &stdout); err != nil {
		t.Fatalf("register existing layout reference: %v", err)
	}
	metadata := readWorkerImageMetadata(t, outputPath)
	if metadata.References[0] != reference || metadata.References[1] != secondReference {
		t.Fatalf("registered references = %#v", metadata.References)
	}
	if metadata.Architecture != "amd64" || metadata.Source != "worker-oci" || metadata.Format != "oci" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if metadata.ID == "" || metadata.Path != outputPath {
		t.Fatalf("metadata identity = %#v", metadata)
	}
}

func TestOCIImageContentSizeIncludesLayerContent(t *testing.T) {
	layer := static.NewLayer([]byte("shared OCI layer content"), types.OCILayer)
	image, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}
	contentSize, err := ociImageContentSize(image)
	if err != nil {
		t.Fatal(err)
	}
	manifestSize, err := image.Size()
	if err != nil {
		t.Fatal(err)
	}
	if contentSize <= manifestSize {
		t.Fatalf("content size = %d, manifest size = %d; layer bytes were not counted", contentSize, manifestSize)
	}
}

func TestRunImageToOCIRegistersLayoutCreatedWithoutWorkerMetadata(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "skopeo-layout")
	reference := "example.test/agentbox:skopeo"
	config, err := empty.Image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config = config.DeepCopy()
	config.Architecture = "amd64"
	config.OS = "linux"
	image, err := mutate.ConfigFile(empty.Image, config)
	if err != nil {
		t.Fatal(err)
	}
	index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: image})
	index = mutate.IndexMediaType(index, types.OCIImageIndex)
	if _, err := layout.Write(outputPath, index); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runImageToOCI([]string{
		"--output", outputPath,
		"--reference", reference,
	}, &stdout); err != nil {
		t.Fatalf("register OCI layout without Worker metadata: %v", err)
	}
	metadata := readWorkerImageMetadata(t, outputPath)
	if len(metadata.References) != 1 || metadata.References[0] != reference {
		t.Fatalf("registered references = %#v", metadata.References)
	}
	if metadata.Architecture != "amd64" || metadata.Path != outputPath || metadata.Source != "worker-oci" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestRunImageToOCIRequiresArchiveForMissingLayout(t *testing.T) {
	err := runImageToOCI([]string{
		"--output", filepath.Join(t.TempDir(), "missing"),
		"--reference", "alpine:latest",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runImageToOCI() error = nil, want missing archive error")
	}
}

func readWorkerImageMetadata(t *testing.T, layoutPath string) workerImageMetadata {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(layoutPath, workerImageMetadataName))
	if err != nil {
		t.Fatal(err)
	}
	var metadata workerImageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}
