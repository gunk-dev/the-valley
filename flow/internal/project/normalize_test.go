package project

import (
	"testing"
)

func TestNormalizeRepoName(t *testing.T) {
	reg := &Registry{
		Projects: map[string]string{
			"site":  "/home/user/src/site",
			"klaus":  "/home/user/src/klaus",
		},
	}

	tests := []struct {
		name string
		ref  string
		reg  *Registry
		want string
	}{
		{
			name: "project name stays as-is",
			ref:  "site",
			reg:  reg,
			want: "site",
		},
		{
			name: "owner/repo matching project returns project name",
			ref:  "maintainer/site",
			reg:  reg,
			want: "site",
		},
		{
			name: "owner/repo not matching project stays as owner/repo",
			ref:  "maintainer/other-thing",
			reg:  reg,
			want: "maintainer/other-thing",
		},
		{
			name: "full HTTPS URL matching project returns project name",
			ref:  "https://github.com/maintainer/site",
			reg:  reg,
			want: "site",
		},
		{
			name: "full HTTPS URL with .git matching project",
			ref:  "https://github.com/maintainer/site.git",
			reg:  reg,
			want: "site",
		},
		{
			name: "SSH URL matching project returns project name",
			ref:  "git@github.com:maintainer/klaus.git",
			reg:  reg,
			want: "klaus",
		},
		{
			name: "full URL not matching project returns owner/repo",
			ref:  "https://github.com/someorg/newrepo",
			reg:  reg,
			want: "someorg/newrepo",
		},
		{
			name: "empty ref returns empty",
			ref:  "",
			reg:  reg,
			want: "",
		},
		{
			name: "nil registry with project-like name",
			ref:  "site",
			reg:  nil,
			want: "site",
		},
		{
			name: "nil registry with owner/repo",
			ref:  "maintainer/site",
			reg:  nil,
			want: "maintainer/site",
		},
		{
			name: "owner/repo with trailing slash",
			ref:  "maintainer/site/",
			reg:  reg,
			want: "site",
		},
		{
			name: "HTTP URL (not HTTPS)",
			ref:  "http://github.com/maintainer/site",
			reg:  reg,
			want: "site",
		},
		{
			name: "unknown bare name with no registry match",
			ref:  "unknown-project",
			reg:  reg,
			want: "unknown-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRepoName(tt.ref, tt.reg)
			if got != tt.want {
				t.Errorf("NormalizeRepoName(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}
