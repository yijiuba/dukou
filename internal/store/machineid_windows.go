//go:build windows

package store

import "golang.org/x/sys/windows/registry"

// machineID 取 Windows MachineGuid（系统安装时生成，重装系统才会变）。
func machineID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	return v, nil
}
