package envref

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var refPattern = regexp.MustCompile(`\$\{\{\s*([a-zA-Z0-9_-]+)\.([a-zA-Z0-9_]+)\s*\}\}`)

// ParseDotEnv parses a raw .env string into key-value pairs, preserving order by returning sorted keys separately when needed.
// Supports comments (#), optional `export ` prefix, and single/double quotes.
func ParseDotEnv(raw string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")
		trimmed = strings.TrimSpace(trimmed)

		eqIdx := strings.IndexByte(trimmed, '=')
		if eqIdx <= 0 {
			return nil, fmt.Errorf("%d번째 줄 형식이 올바르지 않습니다 (KEY=VALUE 형태여야 함): %q", idx+1, line)
		}
		key := strings.TrimSpace(trimmed[:eqIdx])
		if key == "" {
			return nil, fmt.Errorf("%d번째 줄의 환경변수 키가 비어 있습니다", idx+1)
		}
		val := strings.TrimSpace(trimmed[eqIdx+1:])
		if len(val) >= 2 {
			if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
				(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
				val = val[1 : len(val)-1]
			}
		}
		result[key] = val
	}
	return result, nil
}

// FormatDotEnv renders a key-value map into sorted KEY=VALUE lines.
func FormatDotEnv(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := vars[k]
		if strings.ContainsAny(v, " \t\n#\"'") {
			b.WriteString(fmt.Sprintf("%s=%q\n", k, v))
		} else {
			b.WriteString(fmt.Sprintf("%s=%s\n", k, v))
		}
	}
	return b.String()
}

// ContainsReference checks if value uses ${{Service.KEY}} reference syntax.
func ContainsReference(val string) bool {
	return refPattern.MatchString(val)
}

// ResolveServiceEnv resolves environment variables for targetService by merging:
// 1) shared environment variables, 2) service-specific variables, and
// 3) expanding ${{serviceName.KEY}} references across all services in the same environment.
func ResolveServiceEnv(
	targetService string,
	sharedVars map[string]string,
	serviceVars map[string]map[string]string,
) (map[string]string, error) {
	merged := make(map[string]string)
	for k, v := range sharedVars {
		merged[k] = v
	}
	for k, v := range serviceVars[targetService] {
		merged[k] = v
	}

	resolved := make(map[string]string, len(merged))
	visiting := make(map[string]bool)

	var resolveOne func(svc, key string) (string, error)
	resolveOne = func(svc, key string) (string, error) {
		visitKey := svc + "." + key
		if visiting[visitKey] {
			return "", fmt.Errorf("시크릿 참조 순환이 감지되었습니다: %s", visitKey)
		}

		raw, ok := lookupRaw(svc, key, targetService, sharedVars, serviceVars)
		if !ok {
			return "", fmt.Errorf("참조 대상 시크릿을 찾을 수 없습니다: ${{%s.%s}}", svc, key)
		}
		if !refPattern.MatchString(raw) {
			return raw, nil
		}

		visiting[visitKey] = true
		defer delete(visiting, visitKey)

		var resolveErr error
		out := refPattern.ReplaceAllStringFunc(raw, func(match string) string {
			if resolveErr != nil {
				return match
			}
			parts := refPattern.FindStringSubmatch(match)
			if len(parts) != 3 {
				return match
			}
			val, err := resolveOne(parts[1], parts[2])
			if err != nil {
				resolveErr = err
				return match
			}
			return val
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		return out, nil
	}

	for k := range merged {
		val, err := resolveOne(targetService, k)
		if err != nil {
			return nil, err
		}
		resolved[k] = val
	}
	return resolved, nil
}

func lookupRaw(
	svc, key, targetService string,
	sharedVars map[string]string,
	serviceVars map[string]map[string]string,
) (string, bool) {
	if svcMap, ok := serviceVars[svc]; ok {
		if v, exists := svcMap[key]; exists {
			return v, true
		}
	}
	if svc == targetService {
		if v, exists := sharedVars[key]; exists {
			return v, true
		}
	}
	return "", false
}
