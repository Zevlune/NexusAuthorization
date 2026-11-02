// Package runenv defines the shared Luck runtime environment contract.
package runenv

import (
	"fmt"
	"os"
	"strings"
)

const (
	EnvironmentKey = "LUCKENV"
	ServiceKey     = "SERVICEUK"
)

// Environment 是进程启动时确定的运行环境和服务唯一标识。
type Environment struct {
	Name      string
	ServiceUK string
}

// Load 从进程环境变量读取必填值，不从配置文件推导。
func Load() (Environment, error) {
	result := Environment{
		Name:      strings.TrimSpace(os.Getenv(EnvironmentKey)),
		ServiceUK: strings.TrimSpace(os.Getenv(ServiceKey)),
	}
	var missing []string
	if result.Name == "" {
		missing = append(missing, EnvironmentKey)
	}
	if result.ServiceUK == "" {
		missing = append(missing, ServiceKey)
	}
	if len(missing) != 0 {
		return Environment{}, fmt.Errorf("required environment variables are missing or blank: %s", strings.Join(missing, ", "))
	}
	return result, nil
}
