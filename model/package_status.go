package model

import "time"

type PackageStatus struct {
	Id           string
	Status       string
	LastModified time.Time
}
