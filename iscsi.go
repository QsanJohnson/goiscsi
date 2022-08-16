// @2022 QSAN Inc. All right reserved

package goiscsi

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/glog"
)

type ISCSIUtil struct {
	Opts ISCSIOptions
}

type ISCSIOptions struct {
	Timeout time.Duration // Millisecond
}

type Chap struct {
	User, Passwd string
}

type Target struct {
	Portal string
	Name   string
	Lun    uint64
	Chap   *Chap
}

type Device struct {
	Name, Size            string
	Type, State           string
	Vendor, Model, Serial string
}

type Disk struct {
	Valid                 bool
	Status                string
	Name, Size            string
	Vendor, Model, Serial string
	MpathCnt, DiskCnt     int
	Devices               map[string]*Device
}

type Session struct {
	Portal      string
	Target      string
	State       string
	SCSIDevices []*SCSIDevice
}

type SCSIDevice struct {
	Lun   uint64
	Name  string
	State string
}

const (
	defaultPort        = "3260"
	deviceRetryCnt     = 50
	deviceRetryTimeout = 100 // Millisecond
	dmRetryCnt         = 30
	dmRetryTimeout     = 100 // Millisecond
)

func (iscsi *ISCSIUtil) Login(targets []*Target) error {
	ctx := context.Background()
	var cancel context.CancelFunc
	if iscsi.Opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), iscsi.Opts.Timeout*time.Millisecond)
		defer cancel()
	}

	for _, target := range targets {
		baseArgs := []string{"-m", "node", "-T", target.Name, "-p", target.Portal}
		if _, err := execCmd("iscsiadm", append(baseArgs, []string{"-o", "new"}...)...); err != nil {
			return fmt.Errorf("Failed to new node, err: %v", err)
		}

		if target.Chap != nil {
			if _, err := execCmd("iscsiadm", append(baseArgs, []string{"-o", "update",
				"-n", "node.session.auth.authmethod", "-v", "CHAP",
				"-n", "node.session.auth.username", "-v", target.Chap.User,
				"-n", "node.session.auth.password", "-v", target.Chap.Passwd}...)...); err != nil {

				return fmt.Errorf("Failed to set CHAP config, err: %v", err)
			}
		}

		if _, err := execCmdContext(ctx, "iscsiadm", append(baseArgs, []string{"-l"}...)...); err != nil {
			return fmt.Errorf("Failed to login, err: %v", err)
		}
	}

	return nil
}

func (iscsi *ISCSIUtil) Logout(targets []*Target) error {
	ctx := context.Background()
	var cancel context.CancelFunc
	if iscsi.Opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), iscsi.Opts.Timeout*time.Millisecond)
		defer cancel()
	}

	for _, target := range targets {
		baseArgs := []string{"-m", "node", "-T", target.Name, "-p", target.Portal}
		if _, err := execCmdContext(ctx, "iscsiadm", append(baseArgs, []string{"-u"}...)...); err != nil {
			return fmt.Errorf("Failed to logout, err: %v", err)
		}

		if _, err := execCmd("iscsiadm", append(baseArgs, []string{"-o", "delete"}...)...); err != nil {
			return fmt.Errorf("Failed to delete node, err: %v", err)
		}
	}

	return nil
}

func (iscsi *ISCSIUtil) GetSession() []*Session {
	return getSessions()
}

func (iscsi *ISCSIUtil) GetDisk(targets []*Target) (*Disk, error) {
	sessions := getSessions()

	var devMap map[string]*Device
	var diskCnt, mpathCnt int
	// Wait dm device path ready
	for retries := 1; retries <= dmRetryCnt; retries++ {
		diskCnt, mpathCnt = 0, 0
		devMap, _ = getDevices(sessions, targets)
		for _, dev := range devMap {
			if dev.Type == "disk" {
				diskCnt++
			} else if dev.Type == "mpath" {
				mpathCnt++
			}
		}

		if mpathCnt == 0 && diskCnt > 0 {
			glog.V(2).Infof("[GetDisk] sleep %d msec then try again, retries=%d\n", dmRetryTimeout, retries)
			time.Sleep(time.Millisecond * dmRetryTimeout)
		} else {
			break
		}
	}

	// Collect all device information to Disk structure
	var vendor, model, serial string
	var diskRunningNum int
	diskMatch := true
	disk := &Disk{}
	disk.DiskCnt = diskCnt
	disk.MpathCnt = mpathCnt
	disk.Devices = devMap
	for name, dev := range devMap {
		if dev.Type == "disk" {
			if vendor == "" {
				vendor, model, serial = dev.Vendor, dev.Model, dev.Serial
			} else {
				if vendor != dev.Vendor || model != dev.Model || serial != dev.Serial {
					diskMatch = false
				}
			}

			if dev.State == "running" {
				diskRunningNum++
			}
		} else if dev.Type == "mpath" {
			disk.Name = name
			disk.Size = dev.Size
		}
	}

	if diskMatch {
		disk.Vendor, disk.Model, disk.Serial = vendor, model, serial
	}

	if disk.MpathCnt == 1 && diskMatch {
		disk.Valid = true
	} else if disk.MpathCnt == 0 && disk.DiskCnt == 1 {
		disk.Valid = true
		// If no multipath, assign first device information with disk type to Disk structure
		for name, dev := range devMap {
			if dev.Type == "disk" {
				disk.Name = name
				disk.Size = dev.Size
			}
		}
	}

	if disk.Valid && disk.DiskCnt > 0 {
		if disk.DiskCnt == diskRunningNum {
			disk.Status = "good"
		} else if diskRunningNum == 0 {
			disk.Status = "fail"
		} else {
			disk.Status = "degrade"
		}
	}

	return disk, nil
}
