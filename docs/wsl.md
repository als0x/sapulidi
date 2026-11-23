# sapulidi on WSL

WSL is treated as Linux. The Linux build, the Linux platform layer and the Linux rules apply unchanged; there is no
separate WSL code path.

## What sapulidi touches

- Only the Linux home of the user and the Linux temp directory, as on any Linux host.
- `/mnt/*` is never touched. Those paths are the Windows drives, on another device. Rule S4 of the spec refuses any
  candidate on a different device from its base, so nothing under `/mnt/c` or any other drive is removed.

## Scheduling

systemd may be off in a WSL distribution. Then there is no systemd user timer, and the scheduler uses cron when
`crontab` is available, or a run at session start otherwise (spec section 10). Neither needs root.

## Disk space on the Windows side

WSL keeps the Linux file system in a virtual disk file, `ext4.vhdx`, on the Windows drive. Deleting files inside WSL
frees space inside the disk file, but the file on Windows does not shrink by itself. Shrinking it is a Windows-side
step that sapulidi does not perform and cannot perform.

sapulidi never runs these commands. The user can run them by hand, from a Windows PowerShell or terminal, after
closing all WSL sessions:

```powershell
wsl --shutdown
Optimize-VHD -Path "<path to ext4.vhdx>" -Mode Full
```

Without the Hyper-V PowerShell module, the `diskpart` route works instead:

```
diskpart
select vdisk file="<path to ext4.vhdx>"
attach vdisk readonly
compact vdisk
detach vdisk
exit
```

Run these only by hand and only after `wsl --shutdown`. Back up the distribution first.
