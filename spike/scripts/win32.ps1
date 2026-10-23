# 供其它测试脚本 dot-source 的 Win32 辅助函数：按进程找顶层窗口、判断可见性、发送 WM_CLOSE、读取前台窗口。
if (-not ("SpikeWin32" -as [type])) {
Add-Type -TypeDefinition @"
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;

public static class SpikeWin32 {
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc cb, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hWnd);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetWindowText(IntPtr hWnd, StringBuilder sb, int max);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetClassName(IntPtr hWnd, StringBuilder sb, int max);
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hWnd, uint msg, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint msg, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int cmd);

    public static List<IntPtr> WindowsOf(uint pid) {
        var list = new List<IntPtr>();
        EnumWindows((h, l) => { uint p; GetWindowThreadProcessId(h, out p); if (p == pid) list.Add(h); return true; }, IntPtr.Zero);
        return list;
    }
    public static string Title(IntPtr h) { var sb = new StringBuilder(512); GetWindowText(h, sb, 512); return sb.ToString(); }
    public static string Class(IntPtr h) { var sb = new StringBuilder(512); GetClassName(h, sb, 512); return sb.ToString(); }
}
"@
}

function Get-AppWindow([int]$ProcessId, [string]$TitleLike) {
    foreach ($h in [SpikeWin32]::WindowsOf([uint32]$ProcessId)) {
        if ([SpikeWin32]::Title($h) -like $TitleLike) { return $h }
    }
    return [IntPtr]::Zero
}

function Wait-Until([scriptblock]$Cond, [int]$TimeoutMs = 10000, [int]$StepMs = 50) {
    $sw = [Diagnostics.Stopwatch]::StartNew()
    while ($sw.ElapsedMilliseconds -lt $TimeoutMs) {
        if (& $Cond) { return $true }
        Start-Sleep -Milliseconds $StepMs
    }
    return $false
}
