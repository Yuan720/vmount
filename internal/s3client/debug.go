package s3client

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const debugLogPath = "C:\\vmount-debug.log"

var (
	debugMu sync.Mutex
	debugF  *os.File
)

func debugf(format string, args ...interface{}) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugF == nil {
		f, err := os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		debugF = f
	}
	fmt.Fprintf(debugF, "%s "+format+"\n", append([]interface{}{time.Now().Format("15:04:05.000")}, args...)...)
	debugF.Sync()
}
