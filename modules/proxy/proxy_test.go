// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package proxy

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"code.gitea.io/gitea/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetMatchers() {
	once = sync.Once{}
	hostMatchers = nil
	noProxyMatchers = nil
}

func TestMatchNoProxyExclusion(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{"localhost", "127.0.0.*", "*.internal"}

	assert.False(t, Match("localhost"), "localhost should be excluded by NO_PROXY")
	assert.False(t, Match("127.0.0.1"), "127.0.0.1 should be excluded by NO_PROXY")
	assert.True(t, Match("github.com"), "github.com should be proxied when PROXY_HOSTS is empty")
	assert.False(t, Match("foo.internal"), "*.internal should be excluded by NO_PROXY")
}

func TestMatchProxyHostsWhitelistMode(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{"*.github.com", "api.example.com"}
	setting.Proxy.NoProxy = []string{}

	assert.True(t, Match("api.github.com"), "should proxy *.github.com")
	assert.True(t, Match("api.example.com"), "should proxy api.example.com")
	assert.False(t, Match("google.com"), "should not proxy google.com when PROXY_HOSTS is set")
}

func TestMatchProxyAllWhenHostsEmpty(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	assert.True(t, Match("github.com"), "should proxy all hosts when PROXY_HOSTS is empty")
	assert.True(t, Match("example.com"), "should proxy all hosts when PROXY_HOSTS is empty")
}

func TestMatchDisabled(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = false
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	assert.False(t, Match("github.com"), "should not proxy when proxy is disabled")
}

func TestMatchEmptyProxyURL(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = ""
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	assert.False(t, Match("github.com"), "should not match when ProxyURL is empty (uses system env)")
}

func TestProxyFunctionNoProxy(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{"localhost", "127.0.0.*"}

	proxyFn := Proxy()
	require.NotNil(t, proxyFn)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost/path", nil)
	proxyURL, err := proxyFn(req)
	require.NoError(t, err)
	assert.Nil(t, proxyURL, "localhost should not be proxied")

	req2, _ := http.NewRequest(http.MethodGet, "http://github.com/path", nil)
	proxyURL2, err := proxyFn(req2)
	require.NoError(t, err)
	assert.NotNil(t, proxyURL2, "github.com should be proxied")
	assert.Equal(t, "http://proxy:8080", proxyURL2.String())
}

func TestProxyFunctionDisabled(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = false
	proxyFn := Proxy()
	require.NotNil(t, proxyFn)

	req, _ := http.NewRequest(http.MethodGet, "http://github.com/path", nil)
	proxyURL, err := proxyFn(req)
	require.NoError(t, err)
	assert.Nil(t, proxyURL, "proxy should be nil when disabled")
}

func TestNewProxyHTTPClient(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	client := NewProxyHTTPClient()
	assert.NotNil(t, client)
	assert.NotNil(t, client.Transport)
}

func TestEnvWithProxyRawHTTPS(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	envs := EnvWithProxyRaw("https://github.com/user/repo.git")

	hasHTTPSProxy := false
	hasHTTPProxy := false
	for _, e := range envs {
		if strings.HasPrefix(e, "https_proxy=") {
			hasHTTPSProxy = true
		}
		if strings.HasPrefix(e, "http_proxy=") {
			hasHTTPProxy = true
		}
	}
	assert.True(t, hasHTTPSProxy, "should set https_proxy for HTTPS URLs")
	assert.True(t, hasHTTPProxy, "should set http_proxy for HTTPS URLs")
}

func TestEnvWithProxyRawSSHProperURL(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	envs := EnvWithProxyRaw("ssh://git@github.com/user/repo.git")

	hasSSHCmd := false
	for _, e := range envs {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSHCmd = true
			assert.Contains(t, e, "ProxyCommand", "GIT_SSH_COMMAND should contain ProxyCommand")
			assert.Contains(t, e, "proxy:8080", "GIT_SSH_COMMAND should reference proxy host:port")
		}
	}
	assert.True(t, hasSSHCmd, "should set GIT_SSH_COMMAND for ssh:// URLs")
}

func TestEnvWithProxyRawSCPStyle(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{}

	// SCP-style URL: git@github.com:user/repo.git
	envs := EnvWithProxyRaw("git@github.com:user/repo.git")

	hasSSHCmd := false
	for _, e := range envs {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSHCmd = true
		}
	}
	assert.True(t, hasSSHCmd, "should set GIT_SSH_COMMAND for SCP-style SSH URLs")
}

func TestEnvWithProxyRawNoProxyAlwaysSet(t *testing.T) {
	defer resetMatchers()

	setting.Proxy.Enabled = true
	setting.Proxy.ProxyURL = "http://proxy:8080"
	setting.Proxy.ProxyURLFixed, _ = url.Parse("http://proxy:8080")
	setting.Proxy.ProxyHosts = []string{}
	setting.Proxy.NoProxy = []string{"localhost", "127.0.0.*"}

	envs := EnvWithProxyRaw("https://github.com")

	hasNoProxy := false
	for _, e := range envs {
		if strings.HasPrefix(e, "no_proxy=") {
			hasNoProxy = true
		}
	}
	assert.True(t, hasNoProxy, "no_proxy should always be set when NO_PROXY is configured")
}

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		raw    string
		scheme string
		host   string
	}{
		{"https://github.com/user/repo.git", "https", "github.com"},
		{"http://github.com/user/repo.git", "http", "github.com"},
		{"ssh://git@github.com/user/repo.git", "ssh", "github.com"},
		{"git+ssh://git@github.com/user/repo.git", "git+ssh", "github.com"},
		{"git@github.com:user/repo.git", "ssh", "github.com"},
		{"git@gitlab.example.com:group/repo.git", "ssh", "gitlab.example.com"},
		{"user@bitbucket.org:user/repo.git", "ssh", "bitbucket.org"},
	}
	for _, tt := range tests {
		scheme, host := parseRemoteURL(tt.raw)
		assert.Equal(t, tt.scheme, scheme, "scheme for %q", tt.raw)
		assert.Equal(t, tt.host, host, "host for %q", tt.raw)
	}
}

func TestBuildSSHGitProxyCommand(t *testing.T) {
	defer resetMatchers()

	tests := []struct {
		proxyURL string
		contains string
	}{
		{"http://proxy.example.com:8080", "nc -X connect -x proxy.example.com:8080"},
		{"https://proxy.example.com:8443", "nc -X connect -x proxy.example.com:8443"},
		{"socks5://proxy.example.com:1080", "nc -X 5 -x proxy.example.com:1080"},
		{"socks://proxy.example.com:1080", "nc -X 4 -x proxy.example.com:1080"},
	}
	for _, tt := range tests {
		setting.Proxy.ProxyURLFixed, _ = url.Parse(tt.proxyURL)
		cmd := buildSSHGitProxyCommand()
		assert.Contains(t, cmd, tt.contains, "proxy command for %s", tt.proxyURL)
	}
}
