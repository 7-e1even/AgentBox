package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"
	"unicode"
)

const automationRenderedInputLimit = 256 << 10

func validateAutomationTemplate(source string) error {
	_, err := template.New("sandbox-input").Funcs(automationTemplateFunctions()).Option("missingkey=error").Parse(source)
	if err != nil {
		return fmt.Errorf("自动化表达式无效: %w", err)
	}
	return nil
}

func renderAutomationPatch(source string, context map[string]any) (map[string]any, error) {
	parsed, err := template.New("sandbox-input").Funcs(automationTemplateFunctions()).Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("自动化表达式无效: %w", err)
	}
	var output limitedBuffer
	output.limit = automationRenderedInputLimit
	if err := parsed.Execute(&output, context); err != nil {
		return nil, fmt.Errorf("自动化表达式执行失败: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.UseNumber()
	var patch map[string]any
	if err := decoder.Decode(&patch); err != nil {
		return nil, fmt.Errorf("自动化表达式必须渲染为 JSON 对象: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("自动化表达式只能渲染一个 JSON 对象")
	}
	return patch, nil
}

func mergeAutomationPatch(target, patch map[string]any) map[string]any {
	result := cloneMap(target)
	for key, patchValue := range patch {
		if patchValue == nil {
			delete(result, key)
			continue
		}
		patchMap, patchIsMap := patchValue.(map[string]any)
		targetMap, targetIsMap := result[key].(map[string]any)
		if patchIsMap {
			if !targetIsMap {
				targetMap = map[string]any{}
			}
			result[key] = mergeAutomationPatch(targetMap, patchMap)
			continue
		}
		result[key] = patchValue
	}
	return result
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	if result == nil {
		result = map[string]any{}
	}
	return result
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		return 0, errors.New("自动化表达式渲染结果超过 256 KiB")
	}
	return b.Buffer.Write(data)
}

func automationTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"default": func(fallback, value any) any {
			if isEmptyTemplateValue(value) {
				return fallback
			}
			return value
		},
		"coalesce": func(values ...any) any {
			for _, value := range values {
				if !isEmptyTemplateValue(value) {
					return value
				}
			}
			return ""
		},
		"lower":      func(value any) string { return strings.ToLower(fmt.Sprint(value)) },
		"upper":      func(value any) string { return strings.ToUpper(fmt.Sprint(value)) },
		"trim":       func(value any) string { return strings.TrimSpace(fmt.Sprint(value)) },
		"trimPrefix": func(prefix string, value any) string { return strings.TrimPrefix(fmt.Sprint(value), prefix) },
		"trimSuffix": func(suffix string, value any) string { return strings.TrimSuffix(fmt.Sprint(value), suffix) },
		"replace": func(old, replacement string, value any) string {
			return strings.ReplaceAll(fmt.Sprint(value), old, replacement)
		},
		"split": func(separator string, value any) []string { return strings.Split(fmt.Sprint(value), separator) },
		"join":  func(separator string, values []string) string { return strings.Join(values, separator) },
		"slug":  automationSlug,
		"toJson": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
		"quote": func(value any) (string, error) {
			encoded, err := json.Marshal(fmt.Sprint(value))
			return string(encoded), err
		},
		"sha256": func(value any) string {
			sum := sha256.Sum256([]byte(fmt.Sprint(value)))
			return hex.EncodeToString(sum[:])
		},
	}
}

func isEmptyTemplateValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return reflected.Len() == 0
	case reflect.Bool:
		return !reflected.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflected.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return reflected.Float() == 0
	}
	return false
}

func automationSlug(value any) string {
	var builder strings.Builder
	lastDash := false
	for _, current := range strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			builder.WriteRune(current)
			lastDash = false
			continue
		}
		if builder.Len() > 0 && !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
