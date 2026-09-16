package main

import (
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://host:3000":            "http://host:3000/v1",
		"http://host:3000/":           "http://host:3000/v1",
		"https://api.deepseek.com/v1": "https://api.deepseek.com/v1",
		"https://api.openai.com/v1/":  "https://api.openai.com/v1",
		"":                            "",
		"https://host/openai/v1":      "https://host/openai/v1",
	}
	for input, want := range cases {
		if got := normalizeBaseURL(input); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestBuildCommitContextTruncate(t *testing.T) {
	commits := []*CommitRecord{
		{Date: "2026-09-16T10:00:00+08:00", Project: "p", Branch: "main", Author: "张三", Message: "较新的一条提交"},
		{Date: "2026-09-15T10:00:00+08:00", Project: "p", Branch: "", Author: "张三", Message: "较旧的一条提交"},
	}
	full := buildCommitContext(commits, 1000)
	if full.Truncated || full.Used != 2 {
		t.Errorf("上限足够时不应截断: %+v", full)
	}
	// 上限压到只放得下第一条
	small := buildCommitContext(commits, 1)
	if small.Used != 0 {
		t.Errorf("上限极小时不应保留任何行，实际 %d", small.Used)
	}
	if !small.Truncated {
		t.Errorf("应标记为已截断")
	}
}