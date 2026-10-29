// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Hardware is the machine a run is on (SETUP.md, "Measurement
// discipline"). It holds no account or host identifiers.
type Hardware struct {
	Arch         string `json:"uname_m"`
	CPU          string `json:"cpu_model"`
	InstanceType string `json:"instance_type,omitempty"` // EC2 only
	GOAMD64      string `json:"goamd64,omitempty"`
	GOARM64      string `json:"goarm64,omitempty"`
	NumCPU       int    `json:"num_cpu"`
}

// Versions are the software a run used.
type Versions struct {
	Go          string `json:"go"`           // the toolchain the native stage uses
	TumbleBuild string `json:"tumble_build"` // the Go that built tumble
	Tumble      string `json:"tumble"`       // tumble commit, "+dirty" if modified
	Giri        string `json:"giri"`         // giri commit, "+dirty" if modified; "" if unused
	GiriPath    string `json:"giri_path,omitempty"`
	// GiriModule is the giri library tumble is linked against (the giri
	// evaluator): its module version, which for a pseudo-version ends in
	// the commit, or the local directory of a replace directive.
	GiriModule string `json:"giri_module,omitempty"`
}

// DetectHardware reads the machine's description.
func DetectHardware() Hardware {
	h := Hardware{Arch: output("uname", "-m"), NumCPU: runtime.NumCPU()}
	switch runtime.GOOS {
	case "darwin":
		h.CPU = output("sysctl", "-n", "machdep.cpu.brand_string")
	case "linux":
		h.CPU = linuxCPU()
	}
	h.GOAMD64 = output("go", "env", "GOAMD64")
	h.GOARM64 = output("go", "env", "GOARM64")
	switch runtime.GOARCH {
	case "amd64":
		h.GOARM64 = ""
	case "arm64":
		h.GOAMD64 = ""
	}
	h.InstanceType = ec2InstanceType()
	return h
}

func linuxCPU() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read-only
	var part string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "model name":
			return strings.TrimSpace(v)
		case "CPU part":
			part = "arm64 CPU part " + strings.TrimSpace(v)
		}
	}
	return part
}

// ec2InstanceType asks the instance metadata service (IMDSv2) for the
// instance type only, with a short timeout; off EC2 it returns "".
func ec2InstanceType() string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	tokReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, "http://169.254.169.254/latest/api/token", nil)
	tokReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	resp, err := http.DefaultClient.Do(tokReq)
	if err != nil {
		return ""
	}
	tok, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://169.254.169.254/latest/meta-data/instance-type", nil)
	req.Header.Set("X-aws-ec2-metadata-token", string(tok))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b))
}

// DetectVersions reads the toolchain, tumble's build, and giri's build
// (from the binary's embedded VCS stamp) when giriPath is set.
func DetectVersions(giriPath string) Versions {
	v := Versions{Go: output("go", "env", "GOVERSION"), TumbleBuild: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		v.Tumble = vcs(bi.Settings)
		for _, d := range bi.Deps {
			if d.Path == "github.com/scttfrdmn/giri" {
				v.GiriModule = d.Version
				if d.Replace != nil {
					v.GiriModule = d.Version + " => " + d.Replace.Path + " " + d.Replace.Version
				}
			}
		}
	}
	if v.Tumble == "" {
		v.Tumble = output("git", "rev-parse", "HEAD")
		if output("git", "status", "--porcelain") != "" {
			v.Tumble += "+dirty"
		}
	}
	if giriPath != "" {
		v.GiriPath = giriPath
		if bi, err := buildInfoOf(giriPath); err == nil {
			v.Giri = vcs(bi)
		}
	}
	return v
}

func vcs(settings []debug.BuildSetting) string {
	var rev string
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" && dirty {
		rev += "+dirty"
	}
	return rev
}

// buildInfoOf reads a binary's build settings with go version -m.
func buildInfoOf(path string) ([]debug.BuildSetting, error) {
	out, err := exec.Command("go", "version", "-m", path).Output()
	if err != nil {
		return nil, err
	}
	var settings []debug.BuildSetting
	for line := range bytes.Lines(out) {
		f := strings.Fields(string(line))
		if len(f) >= 2 && f[0] == "build" {
			k, v, _ := strings.Cut(f[1], "=")
			settings = append(settings, debug.BuildSetting{Key: k, Value: v})
		}
	}
	return settings, nil
}

func output(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LoadAverage is the host's 1-minute load average, or -1 if it cannot be
// read: vm.loadavg on darwin, /proc/loadavg on linux.
func LoadAverage() float64 {
	var text string
	switch runtime.GOOS {
	case "darwin":
		text = strings.Trim(output("sysctl", "-n", "vm.loadavg"), "{} ")
	case "linux":
		b, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return -1
		}
		text = string(b)
	}
	f := strings.Fields(text)
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return -1
	}
	return v
}
