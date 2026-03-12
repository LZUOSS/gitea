// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"code.gitea.io/gitea/modules/glob"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
)

var (
	once            sync.Once
	hostMatchers    []glob.Glob
	noProxyMatchers []glob.Glob
)

func initMatchers() {
	once.Do(func() {
		for _, h := range setting.Proxy.ProxyHosts {
			if g, err := glob.Compile(h); err == nil {
				hostMatchers = append(hostMatchers, g)
			} else {
				log.Error("glob.Compile %s failed: %v", h, err)
			}
		}
		for _, h := range setting.Proxy.NoProxy {
			if g, err := glob.Compile(h); err == nil {
				noProxyMatchers = append(noProxyMatchers, g)
			} else {
				log.Error("glob.Compile %s (NO_PROXY) failed: %v", h, err)
			}
		}
	})
}

func matchesNoProxy(host string) bool {
	for _, v := range noProxyMatchers {
		if v.Match(host) {
			return true
		}
	}
	return false
}

// shouldProxyHost returns true if the given hostname should be proxied,
// considering the enabled state, ProxyHosts whitelist, and NO_PROXY exclusions.
func shouldProxyHost(host string) bool {
	if !setting.Proxy.Enabled || setting.Proxy.ProxyURL == "" || host == "" {
		return false
	}

	initMatchers()

	if matchesNoProxy(host) {
		return false
	}

	if len(setting.Proxy.ProxyHosts) > 0 {
		for _, v := range hostMatchers {
			if v.Match(host) {
				return true
			}
		}
		return false
	}

	return true
}

// parseRemoteURL extracts the scheme and host from a raw git remote URL,
// including SCP-style URLs of the form git@host:path that url.Parse cannot handle.
// Returns scheme (lowercased) and hostname.
func parseRemoteURL(rawURL string) (scheme, host string) {
	// Try standard URL parsing first
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" {
		return strings.ToLower(u.Scheme), u.Hostname()
	}

	// SCP-style: [user@]host:path — no scheme, contains ":" but not "://"
	if !strings.Contains(rawURL, "://") && strings.Contains(rawURL, ":") {
		hostPart := rawURL
		// Strip optional user@ prefix
		if atIdx := strings.Index(hostPart, "@"); atIdx >= 0 {
			hostPart = hostPart[atIdx+1:]
		}
		// Strip :path suffix
		if colonIdx := strings.Index(hostPart, ":"); colonIdx >= 0 {
			return "ssh", hostPart[:colonIdx]
		}
	}

	return "", ""
}

// buildSSHGitProxyCommand returns a GIT_SSH_COMMAND value that tunnels SSH
// through the configured HTTP/SOCKS proxy using netcat (nc). Returns "" if
// the proxy scheme is unsupported or no proxy is configured.
// Requires nc (netcat) with -X CONNECT/SOCKS support on the system.
func buildSSHGitProxyCommand() string {
	if setting.Proxy.ProxyURLFixed == nil {
		return ""
	}
	p := setting.Proxy.ProxyURLFixed
	host := p.Hostname()
	port := p.Port()

	switch strings.ToLower(p.Scheme) {
	case "http", "https":
		if port == "" {
			port = "8080"
		}
		return fmt.Sprintf("ssh -o ProxyCommand='nc -X connect -x %s:%s %%h %%p'", host, port)
	case "socks5", "socks5h":
		if port == "" {
			port = "1080"
		}
		return fmt.Sprintf("ssh -o ProxyCommand='nc -X 5 -x %s:%s %%h %%p'", host, port)
	case "socks", "socks4":
		if port == "" {
			port = "1080"
		}
		return fmt.Sprintf("ssh -o ProxyCommand='nc -X 4 -x %s:%s %%h %%p'", host, port)
	}
	return ""
}

// GetProxyURL returns proxy url
func GetProxyURL() string {
	if !setting.Proxy.Enabled {
		return ""
	}

	if setting.Proxy.ProxyURL == "" {
		if os.Getenv("http_proxy") != "" {
			return os.Getenv("http_proxy")
		}
		return os.Getenv("https_proxy")
	}
	return setting.Proxy.ProxyURL
}

// Match returns true if the hostname needs to be proxied
func Match(u string) bool {
	return shouldProxyHost(u)
}

// Proxy returns the system proxy
func Proxy() func(req *http.Request) (*url.URL, error) {
	if !setting.Proxy.Enabled {
		return func(req *http.Request) (*url.URL, error) {
			return nil, nil
		}
	}
	if setting.Proxy.ProxyURL == "" {
		return http.ProxyFromEnvironment
	}

	initMatchers()

	return func(req *http.Request) (*url.URL, error) {
		// NO_PROXY exclusions take priority
		if matchesNoProxy(req.URL.Host) {
			return nil, nil
		}

		// If PROXY_HOSTS is specified, only proxy matching hosts (whitelist mode)
		if len(hostMatchers) > 0 {
			for _, v := range hostMatchers {
				if v.Match(req.URL.Host) {
					return http.ProxyURL(setting.Proxy.ProxyURLFixed)(req)
				}
			}
			return http.ProxyFromEnvironment(req)
		}

		// PROXY_HOSTS is empty → proxy all hosts
		return http.ProxyURL(setting.Proxy.ProxyURLFixed)(req)
	}
}

// NewProxyHTTPTransport returns an http.Transport configured with the global proxy settings.
func NewProxyHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy: Proxy(),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
		},
	}
}

// NewProxyHTTPClient returns an http.Client configured with the global proxy settings.
func NewProxyHTTPClient() *http.Client {
	return &http.Client{
		Transport: NewProxyHTTPTransport(),
	}
}

// EnvWithProxy returns os.Environ() augmented with proxy env vars for an
// already-parsed *url.URL. For raw URL strings (including SCP-style SSH URLs),
// use EnvWithProxyRaw instead.
func EnvWithProxy(u *url.URL) []string {
	if u == nil {
		return os.Environ()
	}
	return EnvWithProxyRaw(u.String())
}

// EnvWithProxyRaw returns os.Environ() augmented with proxy env vars for the
// given raw remote URL string. Handles HTTP/HTTPS, ssh://, and SCP-style
// git@host:path URLs. SSH proxying requires nc (netcat) with -X support.
func EnvWithProxyRaw(rawURL string) []string {
	envs := os.Environ()

	scheme, host := parseRemoteURL(rawURL)

	switch scheme {
	case "http", "https":
		if shouldProxyHost(host) {
			proxyURL := GetProxyURL()
			envs = append(envs, "https_proxy="+proxyURL)
			envs = append(envs, "http_proxy="+proxyURL)
		}
	case "ssh", "git+ssh", "git":
		if shouldProxyHost(host) {
			if sshCmd := buildSSHGitProxyCommand(); sshCmd != "" {
				envs = append(envs, "GIT_SSH_COMMAND="+sshCmd)
			}
		}
	}

	// Always propagate NO_PROXY to subprocesses when proxy is enabled
	if setting.Proxy.Enabled && len(setting.Proxy.NoProxy) > 0 {
		envs = append(envs, "no_proxy="+strings.Join(setting.Proxy.NoProxy, ","))
	}

	return envs
}
