package api

import "strings"

/**
 * 去空并去重，保持原有顺序。
 *
 * @param items 原始字符串切片
 * @return 清洗后的切片
 * @author Auto
 * @date 2026-10-09
 */
func uniqueNonEmpty(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
