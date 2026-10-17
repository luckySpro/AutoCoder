package main

import (
	"runtime"
	"testing"
)

func TestMergePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("用的是 : 分隔")
	}
	if got := mergePath("/a:/b", "/b::/c", "/a:/d"); got != "/a:/b:/c:/d" {
		t.Fatal(got)
	}
}
