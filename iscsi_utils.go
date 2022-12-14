// @2022 QSAN Inc. All right reserved

package goiscsi

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	kexec "k8s.io/utils/exec"
	mount "k8s.io/utils/mount"
)

func getSessions() []*Session {
	var sessions []*Session

	args := []string{"-m", "session", "-P", "3"}
	out, err := execCmd("iscsiadm", args...)
	if err != nil {
		glog.Warningf("Failed to get session, err: %v", err)
		return sessions
	}

	var curTarget string
	var curSession *Session
	var scsiDev *SCSIDevice
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "Target:"):
			curTarget = strings.Fields(line)[1]
		case strings.HasPrefix(line, "Current Portal:"):
			tmpSession := Session{
				Target: curTarget,
				Portal: strings.Split(sessionFieldValue(line), ",")[0],
			}
			curSession = &tmpSession
			sessions = append(sessions, curSession)
		case strings.HasPrefix(line, "iSCSI Session State:"):
			curSession.State = sessionFieldValue(line)
		case strings.HasPrefix(line, "scsi"):
			lun, _ := strconv.ParseUint(sessionFieldValue(line), 10, 32)
			tmpScsiDev := SCSIDevice{Lun: lun}
			scsiDev = &tmpScsiDev
			curSession.SCSIDevices = append(curSession.SCSIDevices, scsiDev)
		case strings.HasPrefix(line, "Attached scsi disk"):
			scsiDev.Name = strings.Fields(line)[3]
			scsiDev.State = sessionFieldValue(line)
		}
	}

	return sessions
}

func rescanSession(targets []*Target) error {
	if targets == nil {
		args := []string{"-m", "session", "--rescan"}
		if _, err := execCmd("iscsiadm", args...); err != nil {
			return fmt.Errorf("Failed to rescan session, err: %v", err)
		}
	} else {
		for _, target := range targets {
			args := []string{"-m", "node", "-T", target.Name, "--rescan"}
			if _, err := execCmd("iscsiadm", args...); err != nil {
				return fmt.Errorf("Failed to rescan session of target(%s), err: %v", target.Name, err)
			}
		}
	}

	return nil
}

func getDevices(sessions []*Session, targets []*Target) (map[string]*Device, error) {
	var devs []*Device
	devMap := make(map[string]*Device)
	for _, target := range targets {
		devicePath := strings.Join([]string{"/dev/disk/by-path/ip", target.Portal, "iscsi", target.Name, "lun", fmt.Sprint(target.Lun)}, "-")
		glog.V(2).Infof("[getDevices] devicePath=%s \n", devicePath)

		// Wait device path ready if device lun session exists
		exists := false
		for retries := 1; retries <= deviceRetryCnt; retries++ {
			_, err := os.Stat(devicePath)
			if os.IsNotExist(err) && lunSessionExists(sessions, target) {
				glog.V(3).Infof("[getDevices] sleep %d msec then try again, retries=%d (%s)\n", deviceRetryTimeout, retries, devicePath)
				time.Sleep(time.Millisecond * deviceRetryTimeout)
			} else {
				exists = true
				break
			}
		}

		if exists {
			args := []string{"-rn", "-o", "NAME,KNAME,PKNAME,TYPE,STATE,SIZE,VENDOR,MODEL,WWN"}
			out, err := execCmd("lsblk", append(args, []string{devicePath}...)...)
			if err == nil {
				lines := strings.Split(strings.Trim(string(out), "\n"), "\n")
				for _, line := range lines {
					tokens := strings.Split(line, " ")
					glog.V(2).Infof("[getDevices] deviceInfo %+v\n", tokens)
					dev := &Device{
						Name:   tokens[0],
						Type:   tokens[3],
						State:  tokens[4],
						Size:   tokens[5],
						Vendor: tokens[6],
						Model:  tokens[7],
						Serial: tokens[8],
					}
					devs = append(devs, dev)
					devMap[tokens[1]] = dev
				}
			} else {
				fmt.Printf("Failed to get disk path : %v \n", err)
			}
		}
	}

	return devMap, nil
}

func hasMntDevices(targets []*Target) (bool, error) {
	cnt, total := 0, 0
	prefixDir := "/dev/disk/by-path/"

	var devPaths []string
	for _, target := range targets {
		devPrefixName := strings.Join([]string{"ip", target.Portal, "iscsi", target.Name, "lun"}, "-")

		files, err := ioutil.ReadDir(prefixDir)
		if err != nil {
			return false, fmt.Errorf("Failed to ReadDir: %v", err)
		}

		for _, file := range files {
			if strings.HasPrefix(file.Name(), devPrefixName) {
				total++

				args := []string{"-rn", "-o", "NAME,KNAME,MOUNTPOINT"}
				devicePath := prefixDir + file.Name()
				out, err := execCmd("lsblk", append(args, []string{devicePath}...)...)
				if err == nil {
					line := strings.Trim(string(out), "\n")
					tokens := strings.Split(line, " ")
					devPaths = append(devPaths, tokens[1])
					mntPath := tokens[2]
					if len(mntPath) > 0 {
						glog.V(2).Infof("[hasMntDevices] %s, mountpoint(%s)\n", file.Name(), mntPath)
						cnt++
					}
				}
			}
		}
	}

	glog.V(2).Infof("[hasMntDevices] cnt: %d/%d, devPaths: %+v\n", cnt, total, devPaths)

	mounter := &mount.SafeFormatAndMount{
		Interface: mount.New(""),
		Exec:      kexec.New(),
	}
	mnts, err := mounter.List()
	if err != nil {
		glog.V(2).Infof("[hasMntDevices] List mount err: %v\n", err)
	}
	for _, mp := range mnts {
		var devName string
		if mp.Device == "udev" {
			args := []string{"-rn", "-o", "KNAME"}
			out, err := execCmd("lsblk", append(args, []string{mp.Path}...)...)
			if err == nil {
				devName = strings.Trim(string(out), "\n")
			}

		} else if strings.HasPrefix(mp.Device, "/dev/") {
			devName = mp.Device[5:]
		} else {
			continue
		}

		if len(devName) > 0 && contains(devPaths, devName) {
			glog.V(2).Infof("[hasMntDevices] Found %s path(%s) devName(%s)\n", mp.Device, mp.Path, devName)
			return true, nil
		}
	}

	if cnt > 0 {
		return true, nil
	} else {
		return false, nil
	}
}

func lunSessionExists(sessions []*Session, target *Target) bool {
	for _, sess := range sessions {
		if sess.Portal == target.Portal && sess.Target == target.Name {
			for _, scsiDev := range sess.SCSIDevices {
				if scsiDev.Lun == target.Lun {
					return true
				}
			}
		}
	}

	return false
}

func targetSessionExists(sessions []*Session, target *Target) bool {
	for _, sess := range sessions {
		if sess.Portal == target.Portal && sess.Target == target.Name {
			return true
		}
	}

	return false
}
