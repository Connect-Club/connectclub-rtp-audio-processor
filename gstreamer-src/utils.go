package gstreamer_src
import "C"

func boolToInt(v bool) int {
	if v {
		return 1
	} else {
		return 0
	}
}