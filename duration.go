package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type CustomDuration int

func (d *CustomDuration) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	parts := strings.Split(v, ":")
	var seconds int
	var err error

	switch len(parts) {
	case 1:
		seconds, err = strconv.Atoi(parts[0])
	case 2:
		mm, err := strconv.Atoi(parts[0])
		if err != nil {
			return err
		}
		ss, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		seconds = mm*60 + ss
	case 3:
		hh, err := strconv.Atoi(parts[0])
		if err != nil {
			return err
		}
		mm, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		ss, err := strconv.Atoi(parts[2])
		if err != nil {
			return err
		}
		seconds = hh*3600 + mm*60 + ss
	default:
		return fmt.Errorf("invalid time format")
	}

	if err != nil {
		return err
	}

	*d = CustomDuration(seconds)
	return nil
}
