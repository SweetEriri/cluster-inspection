package utils

import (
	"net/http"
	"strconv"
	"time"
)

type TimeRangeQuery struct {
	StartTime    time.Time
	EndTime      time.Time
	Limit        int
	Offset       int
	UseTimeRange bool
}

func ParseTimeRangeQuery(r *http.Request) (TimeRangeQuery, error) {
	query := TimeRangeQuery{
		Limit:  10, // 默认值
		Offset: 0,  // 默认值
	}

	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		startTime, err := ParseLocalTime(startTimeStr)
		if err != nil {
			return query, err
		}
		query.StartTime = startTime
		query.UseTimeRange = true
	}

	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		endTime, err := ParseLocalTime(endTimeStr)
		if err != nil {
			return query, err
		}
		query.EndTime = endTime
		query.UseTimeRange = true
	}

	if !query.UseTimeRange {
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
				query.Limit = parsedLimit
			}
		}
		if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
			if parsedOffset, err := strconv.Atoi(offsetStr); err == nil {
				query.Offset = parsedOffset
			}
		}
	}

	return query, nil
}
