package toolscan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// RemoteURL 是在线参数清单：各 CLI 升级改了参数后，只要更新仓库里的 tools.yaml，
// 已安装的 AutoCoder 下次检查时就会用上新参数，不必等新版本。
const RemoteURL = "https://raw.githubusercontent.com/luckySpro/AutoCoder/main/internal/toolscan/tools.yaml"

// RemotePath 是在线清单的本地缓存，放在用户清单旁边。
func RemotePath(userPath string) string {
	if userPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(userPath), "tools.remote.yaml")
}

// FetchRemote 下载在线清单，校验通过后写入 path；内容没变时 changed 为 false。
func FetchRemote(ctx context.Context, url, path string) (changed bool, err error) {
	if path == "" {
		return false, errors.New("没有缓存路径")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("在线清单返回 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return false, err
	}
	if _, err := parseRemote(data); err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

func parseRemote(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("在线清单格式不对：%w", err)
	}
	if len(m.Tools) == 0 {
		return m, errors.New("在线清单里没有工具")
	}
	for _, t := range m.Tools {
		if t.ID == "" || len(t.Headless) == 0 {
			return m, fmt.Errorf("在线清单条目不完整：%q", t.ID)
		}
	}
	return m, nil
}

// readRemote 读取本地缓存的在线清单；不存在或损坏时返回空清单。
func readRemote(path string) Manifest {
	if path == "" {
		return Manifest{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}
	}
	m, err := parseRemote(data)
	if err != nil {
		return Manifest{}
	}
	return m
}

// applyRemote 用在线清单更新内置条目的调用参数。为安全起见只更新参数与关键字，
// 不改要执行的命令和查找路径，也不新增工具。
func applyRemote(base, remote Manifest) Manifest {
	out := Manifest{Tools: append([]Tool(nil), base.Tools...)}
	for _, r := range remote.Tools {
		for i, t := range out.Tools {
			if t.ID != r.ID {
				continue
			}
			t.VersionArgs = r.VersionArgs
			t.HelpArgs = r.HelpArgs
			t.Headless = r.Headless
			t.UsageArgs = r.UsageArgs
			t.AutoArgs = r.AutoArgs
			t.FullArgs = r.FullArgs
			t.StreamArgs = r.StreamArgs
			t.ReadonlyArgs = r.ReadonlyArgs
			t.AuthErrors = r.AuthErrors
			t.LimitErrors = r.LimitErrors
			out.Tools[i] = t
		}
	}
	return out
}
