package skillimport

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"agentbox/internal/platform"
)

var repositoryComponent = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var catalogSkillComponent = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$`)
var skillSlugSeparators = regexp.MustCompile(`[^a-z0-9]+`)

func skillSlug(name string) string {
	return strings.Trim(skillSlugSeparators.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func skillsSHPathParts(source *url.URL) ([]string, error) {
	parts := strings.Split(strings.Trim(source.Path, "/"), "/")
	if len(parts) != 3 {
		return nil, invalid("请粘贴 skills.sh 的具体 Skill 链接，例如 https://skills.sh/vercel-labs/skills/find-skills；目前支持 GitHub 来源，其他来源请使用文件直链或本地上传")
	}
	for index, part := range parts {
		pattern := repositoryComponent
		if index == 2 {
			pattern = catalogSkillComponent
		}
		if len(part) > 100 || !pattern.MatchString(part) {
			return nil, invalid("skills.sh 链接中的仓库或 Skill 名称无效")
		}
	}
	return parts, nil
}

func fetchSkillsSH(ctx context.Context, client *http.Client, source *url.URL) (Draft, error) {
	parts, err := skillsSHPathParts(source)
	if err != nil {
		return Draft{}, err
	}
	// Read GitHub's public archive without invoking npx/git, installing packages,
	// using catalog credentials, or consuming the GitHub metadata API quota.
	link := &url.URL{Scheme: "https", Host: "codeload.github.com", Path: "/" + parts[0] + "/" + parts[1] + "/zip/HEAD"}
	data, _, err := download(ctx, client, link, 32<<20)
	if err != nil {
		return Draft{}, err
	}
	draft, err := repositorySkill(data, parts[2])
	if err != nil {
		return Draft{}, err
	}
	draft.Spec.Source = "skills.sh"
	draft.Spec.Path = source.String()
	return draft, nil
}

func repositorySkill(data []byte, name string) (Draft, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 50000 {
		return Draft{}, invalid("仓库归档损坏或目录过大，请只打包所需 Skill 后上传")
	}
	var candidates []*zip.File
	for _, file := range archive.File {
		if path.Base(file.Name) == "SKILL.md" && file.Mode().IsRegular() && platform.ValidSkillPath(file.Name) {
			candidates = append(candidates, file)
		}
	}
	var selected *zip.File
	scanned, scannedBytes := 0, 0
	// Directory names often match the catalog slug. If they do not (for example
	// vercel-react-best-practices), resolve the actual frontmatter name instead.
	for _, exactDirectory := range []bool{true, false} {
		for _, file := range candidates {
			if (skillSlug(path.Base(path.Dir(file.Name))) == skillSlug(name)) != exactDirectory {
				continue
			}
			scanned++
			if scanned > 128 || scannedBytes > 8<<20 {
				return Draft{}, invalid("仓库中的 Skill 过多，无法准确定位；请上传所需 Skill 的 ZIP")
			}
			content, err := readZIPFile(file)
			if err != nil {
				return Draft{}, err
			}
			scannedBytes += len(content)
			draft, err := readDocument(content)
			if err != nil || skillSlug(draft.Name) != skillSlug(name) {
				continue
			}
			if selected != nil {
				return Draft{}, invalid("仓库中有多个同名 Skill，请打包具体目录后上传")
			}
			selected = file
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return Draft{}, invalid("公开仓库中未找到该 Skill，来源可能已移动；请使用最新链接或本地上传")
	}
	document, files, err := readSkillDirectory(archive, selected.Name)
	if err != nil {
		return Draft{}, err
	}
	draft, err := readDocument(document)
	if err != nil {
		return Draft{}, err
	}
	draft.Spec.Files = files
	return draft, platform.ValidateSkillSpec(draft.Spec)
}
