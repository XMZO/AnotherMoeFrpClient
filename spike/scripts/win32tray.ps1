# 托盘相关的 Win32 辅助：枚举进程的 message-only 窗口、向 Explorer 查询托盘图标位置、读取弹出菜单内容。
if (-not ("SpikeTray" -as [type])) {
Add-Type -TypeDefinition @"
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;

public static class SpikeTray {
    [StructLayout(LayoutKind.Sequential)]
    public struct NOTIFYICONIDENTIFIER { public uint cbSize; public IntPtr hWnd; public uint uID; public Guid guidItem; }
    [StructLayout(LayoutKind.Sequential)]
    public struct RECT { public int Left, Top, Right, Bottom; }

    [DllImport("shell32.dll")] public static extern int Shell_NotifyIconGetRect(ref NOTIFYICONIDENTIFIER id, out RECT rect);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr after, string cls, string title);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint msg, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hWnd, uint msg, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
    [DllImport("user32.dll")] public static extern int GetMenuItemCount(IntPtr hMenu);
    [DllImport("user32.dll")] public static extern uint GetMenuItemID(IntPtr hMenu, int pos);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetMenuString(IntPtr hMenu, uint item, StringBuilder sb, int max, uint flags);
    [DllImport("user32.dll")] public static extern uint GetMenuState(IntPtr hMenu, uint item, uint flags);

    public static readonly IntPtr HWND_MESSAGE = new IntPtr(-3);

    // 进程拥有的 message-only 窗口（Wails 托盘窗口就是这种）
    public static List<IntPtr> MessageWindowsOf(uint pid, string cls) {
        var list = new List<IntPtr>();
        IntPtr h = IntPtr.Zero;
        while ((h = FindWindowEx(HWND_MESSAGE, h, cls, null)) != IntPtr.Zero) {
            uint p; GetWindowThreadProcessId(h, out p);
            if (p == pid) list.Add(h);
        }
        return list;
    }

    // 系统弹出菜单窗口（类名 #32768）
    public static List<IntPtr> PopupMenusOf(uint pid) {
        var list = new List<IntPtr>();
        IntPtr h = IntPtr.Zero;
        while ((h = FindWindowEx(IntPtr.Zero, h, "#32768", null)) != IntPtr.Zero) {
            uint p; GetWindowThreadProcessId(h, out p);
            if (p == pid && IsWindowVisible(h)) list.Add(h);
        }
        return list;
    }

    public static string IconRect(IntPtr hwnd, uint uid) {
        var id = new NOTIFYICONIDENTIFIER();
        id.cbSize = (uint)Marshal.SizeOf(typeof(NOTIFYICONIDENTIFIER));
        id.hWnd = hwnd; id.uID = uid;
        RECT r;
        int hr = Shell_NotifyIconGetRect(ref id, out r);
        if (hr != 0) return null;
        return String.Format("{0},{1},{2},{3}", r.Left, r.Top, r.Right, r.Bottom);
    }

    public static List<string> MenuItems(IntPtr hMenu) {
        var list = new List<string>();
        int n = GetMenuItemCount(hMenu);
        for (int i = 0; i < n; i++) {
            var sb = new StringBuilder(256);
            GetMenuString(hMenu, (uint)i, sb, 256, 0x400 /*MF_BYPOSITION*/);
            uint st = GetMenuState(hMenu, (uint)i, 0x400);
            list.Add(String.Format("[{0}] id={1} text='{2}' state=0x{3:x}", i, GetMenuItemID(hMenu, i), sb.ToString(), st));
        }
        return list;
    }
}
"@
}

$WM_CLOSE = 0x0010
$WM_KEYDOWN = 0x0100
$WM_KEYUP = 0x0101
$WM_LBUTTONUP = 0x0202
$WM_CONTEXTMENU = 0x007B
$WM_USER_SYSTRAY = 0x0401   # Wails: wmUserSystray = WM_USER + 1
$MN_GETHMENU = 0x01E1
$VK_DOWN = 0x28
$VK_UP = 0x26
$VK_RETURN = 0x0D

function Find-TrayIcon([int]$ProcessId) {
    # Wails 托盘窗口是 WndClass(默认 WailsWebviewWindow) 的 message-only 窗口，uID = SystemTray.id（从 1 开始）
    foreach ($h in [SpikeTray]::MessageWindowsOf([uint32]$ProcessId, "WailsWebviewWindow")) {
        foreach ($uid in 1..8) {
            $rect = [SpikeTray]::IconRect($h, [uint32]$uid)
            if ($rect) { return [pscustomobject]@{ Hwnd = $h; Uid = $uid; Rect = $rect } }
        }
    }
    return $null
}

function Send-TrayEvent($Tray, [int]$MouseMsg) {
    # NOTIFYICON_VERSION_4：lParam 低 16 位 = 鼠标事件，高 16 位 = 图标 ID
    $lp = [IntPtr](($Tray.Uid -shl 16) -bor $MouseMsg)
    [void][SpikeTray]::PostMessage($Tray.Hwnd, $WM_USER_SYSTRAY, [IntPtr]::Zero, $lp)
}

function Send-MenuKey([IntPtr]$MenuHwnd, [int]$Vk) {
    [void][SpikeTray]::PostMessage($MenuHwnd, $WM_KEYDOWN, [IntPtr]$Vk, [IntPtr]::Zero)
    [void][SpikeTray]::PostMessage($MenuHwnd, $WM_KEYUP, [IntPtr]$Vk, [IntPtr]::Zero)
}
