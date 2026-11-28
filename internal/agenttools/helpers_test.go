package agenttools

import "time"

func timeAfter(sec int) <-chan time.Time { return time.After(time.Duration(sec) * time.Second) }
func sleepMs(ms int)                     { time.Sleep(time.Duration(ms) * time.Millisecond) }
