package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/docker/docker/pkg/jsonmessage"
)

const (
	// defaultPullActivityDeadline is the default value set in the imageProgressManager
	// when newImageProgressManager is called
	defaultPullActivityDeadline = 2 * time.Minute

	// defaultImageProgressReportInterval is the default value set in the
	// imageProgressManager when newImageProgressManager is called
	defaultImageProgressReportInterval = 10 * time.Second
)

type layerProgress struct {
	id           string
	status       layerProgressStatus
	currentBytes int64
	totalBytes   int64
}

type layerProgressStatus int

const (
	layerProgressStatusUnknown layerProgressStatus = iota
	layerProgressStatusStarting
	layerProgressStatusWaiting
	layerProgressStatusDownloading
	layerProgressStatusVerifying
	layerProgressStatusDownloaded
	layerProgressStatusExtracting
	layerProgressStatusComplete
	layerProgressStatusExists
)

func lpsFromString(status string) layerProgressStatus {
	switch status {
	case "Pulling fs layer":
		return layerProgressStatusStarting
	case "Waiting":
		return layerProgressStatusWaiting
	case "Downloading":
		return layerProgressStatusDownloading
	case "Verifying Checksum":
		return layerProgressStatusVerifying
	case "Download complete":
		return layerProgressStatusDownloaded
	case "Extracting":
		return layerProgressStatusExtracting
	case "Pull complete":
		return layerProgressStatusComplete
	case "Already exists":
		return layerProgressStatusExists
	default:
		return layerProgressStatusUnknown
	}
}

type imageProgress struct {
	sync.RWMutex
	lastMessage *jsonmessage.JSONMessage
	timestamp   time.Time
	layers      map[string]*layerProgress
	pullStart   time.Time
}

func (p *imageProgress) get() (string, time.Time) {
	p.RLock()
	defer p.RUnlock()

	if p.lastMessage == nil {
		return "No progress", p.timestamp
	}

	var pulled, pulling int
	for _, l := range p.layers {
		if l.status == layerProgressStatusDownloading {
			pulling++
		} else if l.status > layerProgressStatusVerifying {
			pulled++
		}
	}

	elapsed := time.Now().Sub(p.pullStart)
	cur := p.currentBytes()
	total := p.totalBytes()
	var est int64
	if cur != 0 {
		est = (elapsed.Nanoseconds() / cur * total) - elapsed.Nanoseconds()
	}

	return fmt.Sprintf("Pulled %d/%d (%d/%dMB) pulling %d layers - est %.1fs remaining",
		pulled, len(p.layers), cur/1000/1000, total/1000/1000, pulling,
		time.Duration(est).Seconds()), p.timestamp
}

func (p *imageProgress) set(msg *jsonmessage.JSONMessage) {
	p.Lock()
	defer p.Unlock()

	p.lastMessage = msg
	p.timestamp = time.Now()

	lps := lpsFromString(msg.Status)
	if lps == layerProgressStatusUnknown {
		return
	}

	layer, ok := p.layers[msg.ID]
	if !ok {
		layer = &layerProgress{id: msg.ID}
		p.layers[msg.ID] = layer
	}
	layer.status = lps
	if msg.Progress != nil && lps == layerProgressStatusDownloading {
		layer.currentBytes = msg.Progress.Current
		layer.totalBytes = msg.Progress.Total
	} else if lps == layerProgressStatusDownloaded {
		layer.currentBytes = layer.totalBytes
	}
}

func (p *imageProgress) currentBytes() int64 {
	var b int64
	for _, l := range p.layers {
		b += l.currentBytes
	}
	return b
}

func (p *imageProgress) totalBytes() int64 {
	var b int64
	for _, l := range p.layers {
		b += l.totalBytes
	}
	return b
}

type progressReporterFunc func(image string, msg string, timestamp time.Time, pullStart time.Time)

type imageProgressManager struct {
	*imageProgress
	image            string
	activityDeadline time.Duration
	inactivityFunc   progressReporterFunc
	reportInterval   time.Duration
	reporter         progressReporterFunc
	cancel           context.CancelFunc
	stopCh           chan struct{}
	buf              bytes.Buffer
}

func newImageProgressManager(
	image string, cancel context.CancelFunc,
	inactivityFunc, reporter progressReporterFunc) *imageProgressManager {
	return &imageProgressManager{
		image:            image,
		activityDeadline: defaultPullActivityDeadline,
		inactivityFunc:   inactivityFunc,
		reportInterval:   defaultImageProgressReportInterval,
		reporter:         reporter,
		imageProgress: &imageProgress{
			timestamp: time.Now(),
			layers:    make(map[string]*layerProgress),
		},
		cancel: cancel,
		stopCh: make(chan struct{}),
	}
}

func (pm *imageProgressManager) withActivityDeadline(t time.Duration) *imageProgressManager {
	pm.activityDeadline = t
	return pm
}

func (pm *imageProgressManager) withReportInterval(t time.Duration) *imageProgressManager {
	pm.reportInterval = t
	return pm
}

func (pm *imageProgressManager) start() {
	pm.pullStart = time.Now()
	go func() {
		ticker := time.NewTicker(defaultImageProgressReportInterval)
		for {
			select {
			case <-ticker.C:
				msg, timestamp := pm.get()
				if time.Now().Sub(timestamp) > pm.activityDeadline {
					pm.inactivityFunc(pm.image, msg, timestamp, pm.pullStart)
					pm.cancel()
					return
				}
				pm.reporter(pm.image, msg, timestamp, pm.pullStart)
			case <-pm.stopCh:
				return
			}
		}
	}()
}

func (pm *imageProgressManager) stop() {
	close(pm.stopCh)
}

func (pm *imageProgressManager) Write(p []byte) (n int, err error) {
	n, err = pm.buf.Write(p)

	for {
		line, err := pm.buf.ReadBytes('\n')
		if err == io.EOF {
			pm.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}
		var msg jsonmessage.JSONMessage
		err = json.Unmarshal(line, &msg)
		if err != nil {
			return n, err
		}

		if msg.Error != nil {
			return n, msg.Error
		}

		pm.set(&msg)
	}

	return
}
