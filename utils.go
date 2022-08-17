// @2022 QSAN Inc. All right reserved

package goiscsi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

func getSessions() []*Session {
	args := []string{"-m", "session", "-P", "3"}
	out, err := execCmd("iscsiadm", args...)
	if err != nil {
		fmt.Errorf("Failed to get session, err: %v", err)
	}

	var sessions []*Session
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
				glog.V(2).Infof("[getDevices] sleep %d msec then try again, retries=%d (%s)\n", deviceRetryTimeout, retries, devicePath)
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
					glog.V(3).Infof("[getDevices] deviceInfo %+v\n", tokens)
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

func execCmd(name string, args ...string) (string, error) {
	glog.V(3).Infof("[execCmd] %s, args=%+v \n", name, args)
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	glog.V(3).Infof("[execCmd] Output ==>\n%+v\n", string(out))
	if err != nil {
		return "", fmt.Errorf("%s (%s)\n", strings.TrimRight(string(out), "\n"), err)
	}

	return string(out), err
}

func execCmdContext(ctx context.Context, name string, args ...string) (string, error) {
	glog.V(3).Infof("[execCmdContext] %s, args=%+v \n", name, args)
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	glog.V(3).Infof("[execCmdContext] Output ==>\n%+v\n", string(out))
	if err != nil {
		return "", fmt.Errorf("%s (%s)\n", strings.TrimRight(string(out), "\n"), err)
	}

	return string(out), err
}

func sessionFieldValue(s string) string {
	_, value := fieldKeyValue(s, ":")
	return value
}

func fieldKeyValue(s string, sep string) (string, string) {
	var key, value string
	tokens := strings.SplitN(s, sep, 2)
	if len(tokens) > 0 {
		key = strings.Trim(strings.TrimSpace(tokens[0]), sep)
	}
	if len(tokens) > 1 {
		value = replaceEmpty(strings.TrimSpace(tokens[1]))
	}
	return key, value
}

func replaceEmpty(s string) string {
	if s == "<empty>" {
		return ""
	}
	return s
}
