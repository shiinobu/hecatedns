package qlog

import "time"

func Days(dir string, loc *time.Location) ([]time.Time, error) {
	loc = cmpLocation(loc)
	names, err := dayFiles(dir, Filter{}, loc)
	if err != nil {
		return nil, err
	}
	days := make([]time.Time, 0, len(names))
	for _, name := range names {
		if day, ok := logDay(name, loc); ok {
			days = append(days, day)
		}
	}
	return days, nil
}
