package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMergePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("用的是 : 分隔")
	}
	if got := mergePath("/a:/b", "/b::/c", "/a:/d"); got != "/a:/b:/c:/d" {
		t.Fatal(got)
	}
}

func TestKnownDirsNvmNewestFirst(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"v9.11.2", "v22.3.0", "v20.10.0"} {
		if err := os.MkdirAll(filepath.Join(home, ".nvm", "versions", "node", v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0o755)
	var mine []string
	for _, d := range knownDirs(home) {
		if strings.HasPrefix(d, home) {
			mine = append(mine, strings.TrimPrefix(d, home))
		}
	}
	want := []string{"/.volta/bin", "/.nvm/versions/node/v22.3.0/bin", "/.nvm/versions/node/v20.10.0/bin", "/.nvm/versions/node/v9.11.2/bin"}
	if strings.Join(mine, ",") != strings.Join(want, ",") {
		t.Fatalf("%v", mine)
	}
}

func TestShellPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("没有 sh")
	}
	t.Setenv("PATH", "/x/bin:/usr/bin:/bin")
	if got := shellPath("/bin/sh", "-c", 5*time.Second); got != "/x/bin:/usr/bin:/bin" {
		t.Fatalf("%q", got)
	}
}
