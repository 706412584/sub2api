//go:build windows

package clashsub

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mihomo 子进程在 Windows 上默认不受父进程生命周期约束：父进程被
// TerminateProcess（例如 native-control.ps1 的 Stop-Process、任务管理器结束进程）
// 强杀时，Go 的 defer / 优雅关闭不会执行，mihomo 就会变成孤儿进程并继续占用
// sidecar 端口，导致下一次启动的实例全部 bind 失败。
//
// 这里把每个 mihomo 绑定到一个带 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 的
// Job Object：句柄随进程退出（含强杀）自动关闭，内核随即终止 job 内所有进程。

var (
	mihomoJobOnce sync.Once
	mihomoJob     windows.Handle
	mihomoJobErr  error
)

func ensureMihomoJobObject() (windows.Handle, error) {
	mihomoJobOnce.Do(func() {
		handle, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			mihomoJobErr = err
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(
			handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(handle)
			mihomoJobErr = err
			return
		}
		mihomoJob = handle
	})
	return mihomoJob, mihomoJobErr
}

// assignToJobObject 把已启动的子进程纳入 kill-on-close job。
// 失败不阻断启动：即使无法绑定，行为与修复前一致。
func assignToJobObject(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	job, err := ensureMihomoJobObject()
	if err != nil {
		return err
	}
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	return windows.AssignProcessToJobObject(job, proc)
}
