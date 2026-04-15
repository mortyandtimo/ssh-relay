package main

import (
    "embed"
    "fmt"
    "io/fs"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "syscall"
    "unsafe"
)

//go:embed payload/**
var payloadFS embed.FS

const (
    mbOK          = 0x00000000
    mbIconInfo    = 0x00000040
    mbIconWarning = 0x00000030
)

func main() {
    if err := install(); err != nil {
        showMessage("Cloud Relay Publisher 安装失败", err.Error(), mbOK|mbIconWarning)
        os.Exit(1)
    }
}

func install() error {
    installDir, err := resolveInstallDir()
    if err != nil {
        return err
    }
    if err := os.MkdirAll(filepath.Join(installDir, "runtime"), 0o755); err != nil {
        return fmt.Errorf("create runtime dir: %w", err)
    }
    if err := os.MkdirAll(filepath.Join(installDir, "logs"), 0o755); err != nil {
        return fmt.Errorf("create logs dir: %w", err)
    }

    if err := fs.WalkDir(payloadFS, "payload", func(path string, entry fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if path == "payload" {
            return nil
        }
        rel := strings.TrimPrefix(path, "payload/")
        targetPath := filepath.Join(installDir, filepath.FromSlash(rel))
        if entry.IsDir() {
            return os.MkdirAll(targetPath, 0o755)
        }
        if rel == "desktop-config.json" {
            if _, statErr := os.Stat(targetPath); statErr == nil {
                return nil
            }
        }
        data, readErr := payloadFS.ReadFile(path)
        if readErr != nil {
            return fmt.Errorf("read embedded file %s: %w", rel, readErr)
        }
        if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
            return fmt.Errorf("prepare parent dir for %s: %w", rel, err)
        }
        if err := os.WriteFile(targetPath, data, 0o755); err != nil {
            return fmt.Errorf("write %s: %w", rel, err)
        }
        return nil
    }); err != nil {
        return err
    }

    exePath := filepath.Join(installDir, "CloudRelayPublisher.exe")
    cmd := exec.Command(exePath)
    cmd.Dir = installDir
    if err := cmd.Start(); err != nil {
        showMessage("Cloud Relay Publisher 已安装", fmt.Sprintf("已安装到：\n%s\n\n但自动启动失败，请手动打开 CloudRelayPublisher.exe。", installDir), mbOK|mbIconInfo)
        return nil
    }

    showMessage("Cloud Relay Publisher 已安装", fmt.Sprintf("已更新到：\n%s\n\n程序即将启动。", installDir), mbOK|mbIconInfo)
    return nil
}

func resolveInstallDir() (string, error) {
    if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
        return filepath.Join(localAppData, "CloudRelayPublisher"), nil
    }
    home, err := os.UserHomeDir()
    if err != nil {
        return "", fmt.Errorf("resolve home dir: %w", err)
    }
    return filepath.Join(home, "AppData", "Local", "CloudRelayPublisher"), nil
}

func showMessage(title string, body string, flags uintptr) {
    user32 := syscall.NewLazyDLL("user32.dll")
    messageBox := user32.NewProc("MessageBoxW")
    titlePtr, _ := syscall.UTF16PtrFromString(title)
    bodyPtr, _ := syscall.UTF16PtrFromString(body)
    _, _, _ = messageBox.Call(0, uintptr(unsafe.Pointer(bodyPtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
}
