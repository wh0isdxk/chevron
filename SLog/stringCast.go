package SLog

import (
	"fmt"
)

type StringCast interface {
	String() string
}

func asString(str interface{}) string {
	switch v := str.(type) {
	default:
		return fmt.Sprint(str)
	case StringCast:
		return v.String()
	case error:
		return v.Error()
	case string:
		return v
	}
}