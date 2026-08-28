//go:build linux

package main

import (
	"os"
	"strings"
)

// tuneWebKit works around a WebKitGTK + NVIDIA proprietary driver bug that
// renders the window completely blank.
//
// WebKitGTK's DMA-BUF renderer asks for a GBM buffer through the DRM device. On
// the NVIDIA proprietary driver that call frequently fails with
//
//	KMS: DRM_IOCTL_MODE_CREATE_DUMB failed: Permission denied
//	Failed to create GBM buffer of size 1180x820: Permission denied
//
// and the web process then paints nothing at all — the window opens, the title
// is right, and the content area stays white. WEBKIT_DISABLE_DMABUF_RENDERER=1
// falls back to a shared-memory path that works everywhere.
//
// It is applied only when an NVIDIA driver is actually loaded, because the
// fallback gives up hardware-accelerated compositing, and it never overrides a
// value the user set themselves.
func tuneWebKit() {
	const key = "WEBKIT_DISABLE_DMABUF_RENDERER"
	if _, set := os.LookupEnv(key); set {
		return
	}
	if !nvidiaDriverLoaded() {
		return
	}
	_ = os.Setenv(key, "1")
}

func nvidiaDriverLoaded() bool {
	if _, err := os.Stat("/proc/driver/nvidia/version"); err == nil {
		return true
	}
	// Nouveau and the newer nvidia open modules show up as loaded modules.
	raw, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, _, _ := strings.Cut(line, " ")
		if name == "nvidia" || name == "nvidia_drm" {
			return true
		}
	}
	return false
}
