package main

import (
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

var (
	loggerTemplate = log.New()
)

func GetLogger(parentLogger *log.Entry, obj interface{}) *log.Entry {
	var logger *log.Entry

	if parentLogger != nil {
		logger = parentLogger
	} else {
		logger = log.NewEntry(loggerTemplate)
	}

	if obj != nil {
		switch obj.(type) {
		case *v1.Service:
			service := obj.(*v1.Service)

			return logger.WithFields(log.Fields{
				"service":   service.Name,
				"namespace": service.Namespace,
			})
		}
	}

	return logger
}

func SetupLogger(verbosity int) *log.Entry {
	switch verbosity {
	case 0:
		loggerTemplate.SetLevel(log.InfoLevel)
	case 1:
		loggerTemplate.SetLevel(log.DebugLevel)
	case 2:
		fallthrough
	default:
		loggerTemplate.SetLevel(log.TraceLevel)
	}

	return log.NewEntry(loggerTemplate)
}
