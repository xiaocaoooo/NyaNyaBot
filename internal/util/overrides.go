package util

import (
	"regexp"
)

// ApplyOverrides 根据给定的覆写规则列表处理文本
func ApplyOverrides(input string, overrides []Override) string {
	for _, ov := range overrides {
		re, err := regexp.Compile(ov.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(input) {
			// Go 的 Expand 允许使用 $1, ${1}, ${name} 形式的替换
			// 这与 Python 的 \1, \g<1> 或 \g<name> 类似，但 Go 使用 $
			// 这里直接使用 ReplaceAllString，它支持 $ 语法
			return re.ReplaceAllString(input, ov.Replacement)
		}
	}
	return input
}

type Override struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}
