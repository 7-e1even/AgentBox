package platform

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSkillBundleBytes = 4 << 20
	MaxSkillFileBytes   = 1 << 20
	MaxSkillFiles       = 128
)

func ValidSkillPath(value string) bool {
	return value != "." && len(value) <= 240 && fs.ValidPath(value) &&
		!strings.ContainsAny(value, "\\:") && strings.IndexFunc(value, unicode.IsControl) < 0
}

// Validate every resource write as well as imports; clients must not be able to
// bypass archive validation by submitting attachment paths directly to CRUD.
func ValidateSkillSpec(spec SkillSpec) error {
	if len(spec.Instructions) > MaxSkillFileBytes || !utf8.ValidString(spec.Instructions) || strings.ContainsRune(spec.Instructions, 0) {
		return &ValidationError{Message: "SKILL.md 必须是 UTF-8 文本，且不超过 1 MiB"}
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
