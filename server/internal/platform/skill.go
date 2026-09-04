package platform

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxSkillBundleBytes        = 4 << 20
	MaxSkillFileBytes          = 1 << 20
	MaxSkillFiles              = 128
	MaxSkillBindings           = 32
	MaxSandboxSkillBundleBytes = 16 << 20
)

func ValidSkillPath(value string) bool {
	return value != "." && len(value) <= 240 && fs.ValidPath(value) &&
		!strings.ContainsAny(value, "\\:") && strings.IndexFunc(value, unicode.IsControl) < 0
}

type skillFrontmatter struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	License       string `yaml:"license"`
	Compatibility string `yaml:"compatibility"`
}

func parseSkillFrontmatter(document string) (skillFrontmatter, string, error) {
	document = strings.ReplaceAll(strings.TrimPrefix(document, "\ufeff"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(document, "---\n")
	if !ok {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md 需要以 --- 包围的 YAML 元数据开头"}
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok || len(header) > 16<<10 {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md 的 YAML 元数据未正确结束或超过 16 KiB"}
	}
	var metadata skillFrontmatter
	if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md 的 YAML 元数据格式无效"}
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	metadata.License = strings.TrimSpace(metadata.License)
	metadata.Compatibility = strings.TrimSpace(metadata.Compatibility)
	if metadata.Name == "" || !idPattern.MatchString(metadata.Name) || len(metadata.Name) > 64 {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md name 只能包含小写字母、数字和连字符，且不超过 64 个字符"}
	}
	if metadata.Description == "" || utf8.RuneCountInString(metadata.Description) > 1024 {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md description 必填且不能超过 1024 个字符"}
	}
	if strings.TrimSpace(body) == "" {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md 指令正文不能为空"}
	}
	if utf8.RuneCountInString(metadata.License) > 256 || strings.ContainsAny(metadata.License, "\x00\r\n") {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md license 无效"}
	}
	if utf8.RuneCountInString(metadata.Compatibility) > 500 || strings.ContainsRune(metadata.Compatibility, 0) {
		return skillFrontmatter{}, "", &ValidationError{Message: "SKILL.md compatibility 无效"}
	}
	return metadata, document, nil
}

// CanonicalizeSkillSpec derives provenance from the document and overwrites
// client-supplied digest fields. It is used by both imports and ordinary CRUD.
func CanonicalizeSkillSpec(resourceID string, spec *SkillSpec) error {
	metadata, document, err := parseSkillFrontmatter(spec.Instructions)
	if err != nil {
		return err
	}
	if resourceID != "" && metadata.Name != resourceID {
		return &ValidationError{Message: "SKILL.md name 必须与资源标识一致"}
	}
	spec.Instructions = document
	spec.License = metadata.License
	spec.Compatibility = metadata.Compatibility
	spec.Source = sanitizeSkillSource(spec.Source)
	digest, fileCount, decodedBytes, err := SkillBundleSummary(*spec)
	if err != nil {
		return err
	}
	spec.BundleDigest = digest
	spec.FileCount = fileCount
	spec.DecodedBytes = decodedBytes
	return nil
}

func SkillCatalogDescription(spec SkillSpec) (string, error) {
	metadata, _, err := parseSkillFrontmatter(spec.Instructions)
	if err != nil {
		return "", err
	}
	description := []rune(metadata.Description)
	return string(description[:min(len(description), 500)]), nil
}

func sanitizeSkillSource(value string) string {
	value = strings.TrimSpace(value)
	link, err := url.Parse(value)
	if err != nil || link.Scheme == "" || link.Host == "" {
		return value
	}
	link.User = nil
	link.RawQuery = ""
	link.ForceQuery = false
	link.Fragment = ""
	return link.String()
}

func SkillBundleSummary(spec SkillSpec) (digest string, fileCount, decodedBytes int, err error) {
	files := slices.Clone(spec.Files)
	slices.SortFunc(files, func(a, b SkillFile) int { return strings.Compare(a.Path, b.Path) })
	hasher := sha256.New()
	writeDigestEntry := func(name string, executable bool, content []byte) {
		_ = binary.Write(hasher, binary.BigEndian, uint32(len(name)))
		_, _ = hasher.Write([]byte(name))
		if executable {
			_, _ = hasher.Write([]byte{1})
		} else {
			_, _ = hasher.Write([]byte{0})
		}
		_ = binary.Write(hasher, binary.BigEndian, uint64(len(content)))
		_, _ = hasher.Write(content)
	}
	writeDigestEntry("SKILL.md", false, []byte(spec.Instructions))
	decodedBytes = len(spec.Instructions)
	for _, file := range files {
		content, decodeErr := base64.StdEncoding.Strict().DecodeString(file.Content)
		if decodeErr != nil {
			return "", 0, 0, &ValidationError{Message: "Skill 附件编码无效"}
		}
		writeDigestEntry(file.Path, file.Executable, content)
		decodedBytes += len(content)
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil)), len(files) + 1, decodedBytes, nil
}

// SkillListSpec deliberately omits bundle content while preserving the
// catalog and provenance fields needed by list screens.
func SkillListSpec(spec map[string]any) map[string]any {
	result := make(map[string]any)
	for _, key := range []string{
		"version", "category", "source", "path", "license", "compatibility", "bundleDigest",
	} {
		if value, ok := spec[key]; ok {
			result[key] = value
		}
	}
	fileCount, fileCountOK := skillSummaryMetric(spec["fileCount"], 1, MaxSkillFiles)
	decodedBytes, decodedBytesOK := skillSummaryMetric(spec["decodedBytes"], 1, MaxSkillBundleBytes)
	_, digestOK := result["bundleDigest"]
	if fileCountOK && decodedBytesOK && digestOK {
		result["fileCount"] = fileCount
		result["decodedBytes"] = decodedBytes
		return result
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return result
	}
	var typed SkillSpec
	if err := json.Unmarshal(encoded, &typed); err != nil {
		return result
	}
	digest, fileCount, decodedBytes, err := SkillBundleSummary(typed)
	if err != nil {
		return result
	}
	if _, ok := result["bundleDigest"]; !ok {
		result["bundleDigest"] = digest
	}
	result["fileCount"] = fileCount
	result["decodedBytes"] = decodedBytes
	return result
}

func skillSummaryMetric(value any, minimum, maximum int) (int, bool) {
	var metric int
	switch number := value.(type) {
	case int:
		metric = number
	case float64:
		metric = int(number)
		if float64(metric) != number {
			return 0, false
		}
	default:
		return 0, false
	}
	return metric, metric >= minimum && metric <= maximum
}

// Validate every resource write as well as imports; clients must not be able to
// bypass archive validation by submitting attachment paths directly to CRUD.
func ValidateSkillSpec(spec SkillSpec) error {
	if len(spec.Instructions) > MaxSkillFileBytes || !utf8.ValidString(spec.Instructions) || strings.ContainsRune(spec.Instructions, 0) {
		return &ValidationError{Message: "SKILL.md 必须是 UTF-8 文本，且不超过 1 MiB"}
	}
	if _, _, err := parseSkillFrontmatter(spec.Instructions); err != nil {
		return err
	}
	if len(spec.Files) >= MaxSkillFiles {
		return &ValidationError{Message: "一个 Skill 最多包含 128 个文件（含 SKILL.md）"}
	}
	seen := map[string]bool{"skill.md": true}
	total := len(spec.Instructions)
	for _, file := range spec.Files {
		key := strings.ToLower(file.Path)
		if !ValidSkillPath(file.Path) || seen[key] {
			return &ValidationError{Message: fmt.Sprintf("Skill 文件路径无效或重复: %s", file.Path)}
		}
		seen[key] = true
		if len(file.Content) > base64.StdEncoding.EncodedLen(MaxSkillFileBytes) {
			return &ValidationError{Message: "Skill 单个文件不能超过 1 MiB"}
		}
		content, err := base64.StdEncoding.Strict().DecodeString(file.Content)
		if err != nil || len(content) > MaxSkillFileBytes {
			return &ValidationError{Message: "Skill 附件编码无效或超过 1 MiB"}
		}
		total += len(content)
		if total > MaxSkillBundleBytes {
			return &ValidationError{Message: "Skill 解压后的总大小不能超过 4 MiB"}
		}
	}
	for name := range seen {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return &ValidationError{Message: "Skill 中的文件与目录路径冲突"}
			}
		}
	}
	return nil
}

func ValidateSkillResource(resourceID, description string, spec SkillSpec) error {
	if strings.TrimSpace(description) == "" {
		return &ValidationError{Message: "Skill 简介不能为空"}
	}
	if err := ValidateSkillSpec(spec); err != nil {
		return err
	}
	metadata, _, err := parseSkillFrontmatter(spec.Instructions)
	if err != nil {
		return err
	}
	if metadata.Name != resourceID {
		return &ValidationError{Message: "SKILL.md name 必须与资源标识一致"}
	}
	return nil
}
