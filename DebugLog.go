// +build !windows

package main
import "log/syslog"

func DebugLog(msg string) {
	logger, err := syslog.New(syslog.LOG_DEBUG, "intiki");

	if err == nil {
		logger.Notice(msg)
	}
}

