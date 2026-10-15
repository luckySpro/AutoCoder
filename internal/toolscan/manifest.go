package toolscan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// 条目来源。
const (
	SourceBuiltin  = "builtin"
	SourceModified = "modified"
	SourceCustom   = "custom"
)

// Strengths 是可选的擅长方向。
var Strengths = []string{"architecture", "coding", "frontend", "testing", "review"}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Validate 校验界面提交的工具条目。
func Validate(t Tool) error {
	var errs []string
	if !idRe.MatchString(t.ID) {
		errs = append(errs, "标识只能使用小写字母、数字和连字符，最长 40 个字符")
	}
	if strings.TrimSpace(t.Name) == "" {
		errs = append(errs, "名称不能为空")
	}
	if len(t.Commands) == 0 {
		errs = append(errs, "至少填写一个可执行文件名")
	}
	for _, c := range t.Commands {
		if c == "" || strings.ContainsAny(c, `/\ `) {
			errs = append(errs, fmt.Sprintf("可执行文件名 %q 不能为空、不能含路径或空格；绝对路径请填在「安装路径」里", c))
		}
	}
	if len(t.Headless) > 0 && !slices.Contains(t.Headless, "{prompt}") {
		errs = append(errs, "调用模板必须包含 {prompt} 占位符")
	}
	if t.Capability < 0 || t.Capability > 100 {
		errs = append(errs, "能力评分需在 0 到 100 之间")
	}
	for _, s := range t.Strengths {
		if !slices.Contains(Strengths, s) {
			errs = append(errs, fmt.Sprintf("未知的擅长方向 %q", s))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "；"))
	}
	return nil
}

// ReadUserManifest 读取用户清单；文件不存在时返回空清单。
func ReadUserManifest(path string) (Manifest, error) {
	var m Manifest
	if path == "" {
		return m, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("用户清单 %s 解析失败: %w", path, err)
	}
	return m, nil
}

// WriteUserManifest 原子写入用户清单，并把旧文件保留为 .bak。
func WriteUserManifest(path string, m Manifest) error {
	if path == "" {
		return errors.New("未配置用户清单路径")
	}
	data, err := MarshalManifest(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tools-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", old, 0o644)
	}
	return os.Rename(tmp.Name(), path)
}

// MarshalManifest 输出带说明头的 YAML。
func MarshalManifest(m Manifest) ([]byte, error) {
	body, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	head := "# AutoCoder 用户工具清单：由界面维护，按 id 覆盖或追加内置清单。\n"
	return append([]byte(head), body...), nil
}

// Upsert 新增或替换用户清单中的条目。
func Upsert(m Manifest, t Tool) Manifest {
	t.Source = ""
	out := Manifest{}
	replaced := false
	for _, x := range m.Tools {
		if x.ID == t.ID {
			out.Tools = append(out.Tools, t)
			replaced = true
		} else {
			out.Tools = append(out.Tools, x)
		}
	}
	if !replaced {
		out.Tools = append(out.Tools, t)
	}
	return out
}

// Remove 从用户清单中删除条目；对内置工具而言就是恢复默认。
func Remove(m Manifest, id string) Manifest {
	out := Manifest{}
	for _, x := range m.Tools {
		if x.ID != id {
			out.Tools = append(out.Tools, x)
		}
	}
	return out
}

// IsBuiltin 判断 id 是否为内置工具。
func IsBuiltin(id string) bool {
	return slices.ContainsFunc(Builtin().Tools, func(t Tool) bool { return t.ID == id })
}
