//go:build !windows

package clashsub

import "os/exec"

// 非 Windows 平台由内核在父进程退出时向子进程发送 SIGHUP/由调用方的 Stop 负责回收，
// 这里保持空实现，仅保证 Windows 专用逻辑不参与编译。
func assignToJobObject(_ *exec.Cmd) error { return nil }
