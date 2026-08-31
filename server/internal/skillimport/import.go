package skillimport

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"agentbox/internal/platform"
	"gopkg.in/yaml.v3"
)

type Draft struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Spec        platform.SkillSpec `json:"spec"`
}

func invalid(message string) error {
	return &platform.ValidationError{Message: message}
}

// Parse only reads data. No archive files are extracted and no imported code runs.
func Parse(filename string, data []byte) (Draft, error) {
	if len(data) == 0 || len(data) > platform.MaxSkillBundleBytes {
		return Draft{}, invalid("请选择非空文件，文件大小不能超过 4 MiB")
	}
	var document []byte
	var files []platform.SkillFile
	switch strings.ToLower(path.Ext(filename)) {
	case ".md":
		document = data
	case ".zip":
		var err error
		document, files, err = readArchive(data)
		if err != nil {
			return Draft{}, err
		}
	default:
		return Draft{}, invalid("仅支持 SKILL.md（.md）或包含单个 Skill 的 ZIP 文件")
	}
	draft, err := readDocument(document)
	if err != nil {
		return Draft{}, err
	}
	draft.Spec.Files = files
	draft.Spec.Source = "upload"
	draft.Spec.Path = filename
	return draft, platform.ValidateSkillSpec(draft.Spec)
}

func readDocument(data []byte) (Draft, error) {
	if len(data) > platform.MaxSkillFileBytes || !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return Draft{}, invalid("SKILL.md 必须是 UTF-8 文本，且不超过 1 MiB")
	}
	document := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(document, "---\n")
	if !ok {
		return Draft{}, invalid("SKILL.md 需要以 --- 包围的 YAML 元数据开头，包含 name 和 description")
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok || len(header) > 16<<10 {
		return Draft{}, invalid("SKILL.md 的 YAML 元数据未正确结束或超过 16 KiB")
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Metadata    struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
		return Draft{}, invalid("SKILL.md 的 YAML 元数据格式无效")
	}
	name, description := strings.TrimSpace(metadata.Name), strings.TrimSpace(metadata.Description)
	if name == "" || description == "" || strings.TrimSpace(body) == "" {
		return Draft{}, invalid("SKILL.md 必须包含 name、description 和非空指令正文")
	}
	// Keep full metadata in the document, while fitting the catalog display fields.
	nameRunes, descriptionRunes := []rune(name), []rune(description)
	return Draft{
		Name:        string(nameRunes[:min(len(nameRunes), 80)]),
		Description: string(descriptionRunes[:min(len(descriptionRunes), 500)]),
		Spec: platform.SkillSpec{
			Version: metadata.Metadata.Version, Instructions: document,
		},
	}, nil
}

func readArchive(data []byte) ([]byte, []platform.SkillFile, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 2048 {
		return nil, nil, invalid("ZIP 文件损坏或目录项过多")
	}
	var documents []string
	for _, file := range archive.File {
		if !platform.ValidSkillPath(strings.TrimSuffix(file.Name, "/")) ||
			(!file.Mode().IsRegular() && !file.FileInfo().IsDir()) {
			return nil, nil, invalid("ZIP 包含不安全路径、符号链接或特殊文件")
		}
		if path.Base(file.Name) == "SKILL.md" && file.Mode().IsRegular() && !strings.HasPrefix(file.Name, "__MACOSX/") {
			documents = append(documents, file.Name)
		}
	}
	if len(documents) == 0 {
		return nil, nil, invalid("ZIP 中未找到 SKILL.md，请上传包含该文件的 Skill 目录")
	}
	slices.SortFunc(documents, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	if len(documents) > 1 && strings.Count(documents[0], "/") == strings.Count(documents[1], "/") {
		return nil, nil, invalid("ZIP 包含多个 Skill，请只打包需要导入的一个 Skill 目录")
	}
	return readSkillDirectory(archive, documents[0])
}

func readSkillDirectory(archive *zip.Reader, documentPath string) ([]byte, []platform.SkillFile, error) {
	if !platform.ValidSkillPath(documentPath) {
		return nil, nil, invalid("Skill 目录路径无效")
	}
	root := strings.TrimSuffix(documentPath, "SKILL.md")
	var document []byte
	var files []platform.SkillFile
	total := 0
	for _, file := range archive.File {
		name, inside := strings.CutPrefix(file.Name, root)
		if !inside || file.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		if !platform.ValidSkillPath(name) || !file.Mode().IsRegular() {
			return nil, nil, invalid("Skill 包含不安全路径、符号链接或特殊文件")
		}
		if file.UncompressedSize64 > platform.MaxSkillFileBytes || (name != "SKILL.md" && len(files) >= platform.MaxSkillFiles-1) {
			return nil, nil, invalid("Skill 最多 128 个文件，单个文件不超过 1 MiB")
		}
		content, err := readZIPFile(file)
		if err != nil {
			return nil, nil, err
		}
		total += len(content)
		if total > platform.MaxSkillBundleBytes {
			return nil, nil, invalid("Skill 解压后的总大小不能超过 4 MiB")
		}
		if name == "SKILL.md" {
			if document != nil {
				return nil, nil, invalid("ZIP 中存在重复的 SKILL.md")
			}
			document = content
		} else {
			files = append(files, platform.SkillFile{
				Path: name, Content: base64.StdEncoding.EncodeToString(content), Executable: file.Mode()&0111 != 0,
			})
		}
	}
	if document == nil {
		return nil, nil, invalid("ZIP 中无法读取 SKILL.md")
	}
	slices.SortFunc(files, func(a, b platform.SkillFile) int { return strings.Compare(a.Path, b.Path) })
	return document, files, nil
}

func readZIPFile(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > platform.MaxSkillFileBytes {
		return nil, invalid("Skill 单个文件不能超过 1 MiB")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, invalid("无法读取 ZIP 中的文件")
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, platform.MaxSkillFileBytes+1))
	if err != nil || len(content) > platform.MaxSkillFileBytes {
		return nil, invalid("ZIP 文件损坏或单个文件超过 1 MiB")
	}
	return content, nil
}
