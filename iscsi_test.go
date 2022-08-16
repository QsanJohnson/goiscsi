package goiscsi

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

var iscsi *ISCSIUtil
var tgts []*Target

func TestMain(m *testing.M) {
	fmt.Println("------------Start of TestMain--------------")
	flag.Parse()

	logLevelStr := os.Getenv("GOISCSI_LOG_LEVEL")
	logLevel, _ := strconv.Atoi(logLevelStr)
	if logLevel > 0 {
		flag.Set("alsologtostderr", "true")
		flag.Set("v", logLevelStr)
	}

	iscsi = &ISCSIUtil{Opts: ISCSIOptions{Timeout: 5000}}
	// Here is an example to generate a test tgts directly
	// tgts = []*Target{
	// 	{Portal: "192.168.206.50:3260", Name: "iqn.2004-08.com.qsan:xf2026-000d42f58:dev2.ctr1", Lun: 0, Chap: &Chap{User: "johnson", Passwd: "111122223333"}},
	// 	{Portal: "192.168.206.51:3260", Name: "iqn.2004-08.com.qsan:xf2026-000d42f58:dev2.ctr2", Lun: 0, Chap: &Chap{User: "johnson", Passwd: "111122223333"}},
	// }
	tgts = getTestTarget()
	fmt.Printf("Test tgt cnt=%d\n", len(tgts))
	for _, t := range tgts {
		fmt.Printf("  %+v\n", t)
	}

	code := m.Run()
	fmt.Println("------------End of TestMain--------------")
	os.Exit(code)
}

func TestLogin(t *testing.T) {
	err := iscsi.Login(tgts)
	if err != nil {
		t.Fatalf("TestLogin failed: %v", err)
	}
}

func TestGetSession(t *testing.T) {
	sessions := iscsi.GetSession()
	for _, sess := range sessions {
		fmt.Printf("%+v\n", sess)
		for _, scsiDev := range sess.SCSIDevices {
			fmt.Printf("   %+v\n", scsiDev)
		}
	}
}

func TestGetDisk(t *testing.T) {
	disk, err := iscsi.GetDisk(tgts)
	if err != nil {
		t.Fatalf("TestGetDisk failed: %v", err)
	}

	fmt.Printf("Get disk: %+v\n", disk)
	for name, dev := range disk.Devices {
		fmt.Printf("  %s: %+v\n", name, dev)
	}

	if !disk.Valid {
		if len(disk.Devices) == 0 {
			t.Fatalf("TestGetDisk failed: disk not found")
		} else {
			t.Fatalf("TestGetDisk failed: disk is invalid")
		}
	}
}

func TestLogout(t *testing.T) {
	err := iscsi.Logout(tgts)
	if err != nil {
		t.Fatalf("TestLogout failed: %v", err)
	}
}

func getTestTarget() []*Target {
	prop, err := readTestConf("test.conf")
	if err != nil {
		panic("The system cannot find the file: test.conf")
	}

	if (len(prop["PORTALS"]) == 0) || (len(prop["NODES"]) == 0) || (len(prop["LUNS"]) == 0) {
		panic("test.conf format error! The value of PORTAL, NODE or LUN is invalid.")
	}

	portalArr := strings.Split(prop["PORTALS"], ",")
	nodeArr := strings.Split(prop["NODES"], ",")
	lunArr := strings.Split(prop["LUNS"], ",")
	if (len(portalArr) != len(nodeArr)) || (len(nodeArr) != len(lunArr)) {
		panic("test.conf format error! The number of PORTAL, NODE and LUN should be the same.")
	}

	var targets []*Target
	var chap *Chap
	if len(prop["CHAP_USER"]) > 0 && len(prop["CHAP_PASSWD"]) > 0 {
		chap = &Chap{
			User:   prop["CHAP_USER"],
			Passwd: prop["CHAP_PASSWD"],
		}
	}

	for i, _ := range portalArr {
		portal := strings.TrimSpace(portalArr[i])
		tokens := strings.Split(portal, ":")
		if len(tokens) == 1 {
			portal += (":" + defaultPort)
		}

		lun, _ := strconv.ParseUint(strings.TrimSpace(lunArr[i]), 10, 32)
		target := &Target{
			Portal: portal,
			Name:   strings.TrimSpace(nodeArr[i]),
			Lun:    lun,
			Chap:   chap,
		}
		targets = append(targets, target)
	}

	return targets
}

func readTestConf(filename string) (map[string]string, error) {
	configPropertiesMap := map[string]string{}
	if len(filename) == 0 {
		return nil, errors.New("Error reading conf file " + filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if equal := strings.Index(line, "="); equal >= 0 {
			if key := strings.TrimSpace(line[:equal]); len(key) > 0 {
				value := ""
				if len(line) > equal {
					value = strings.TrimSpace(line[equal+1:])
				}
				configPropertiesMap[key] = value
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	return configPropertiesMap, nil
}
