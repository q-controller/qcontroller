package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/q-controller/qapi-client/src/client"
	"github.com/q-controller/qapi-client/src/monitor"
	"github.com/q-controller/qcontroller/src/generated/qapi"
	"github.com/q-controller/qcontroller/src/generated/qga"
)

type Greeting struct {
	QMP struct {
		Version      qapi.VersionInfo       `json:"version"`
		Capabilities qapi.QMPCapabilityList `json:"capabilities"`
	} `json:"QMP"`
}

var ErrNotReady = errors.New("instance is not ready")

const (
	PREFIX_QGA = "qga"
	PREFIX_QMP = "qmp"
)

type Status struct {
	Id    string
	Ready bool
}

type InstanceMonitor struct {
	qapi    *monitor.Monitor
	ready   map[string]bool
	readyCh chan Status
}

func (i *InstanceMonitor) Execute(name string, request client.Request) (*monitor.ExecuteResult, error) {
	ready, exists := i.ready[name]
	if exists && ready {
		res, resErr := i.qapi.Execute(name, request)
		if resErr != nil {
			return nil, resErr
		}
		return res, nil
	}

	return nil, ErrNotReady
}

func (i *InstanceMonitor) Add(id, socketPath, prefix string, retryCount int, intervalMs int) error {
	if retryCount < 0 || intervalMs < 0 {
		return fmt.Errorf("instancemonitor.add: wrong input")
	}
	name := id
	if prefix != "" {
		name = fmt.Sprintf("%s:%s", prefix, name)
	}
	for range retryCount {
		if addErr := i.qapi.Add(name, socketPath); addErr != nil {
			if err, errOk := <-addErr; errOk && err != nil {
				slog.Error("Could not add instance", "instance", name, "error", err)
				time.Sleep(time.Duration(intervalMs) * time.Millisecond)
				continue
			}
		}

		slog.Info("Added new instance", "instance", name)

		if prefix == PREFIX_QGA {
			go func() {
				i.PingQga(name, 6, 5*time.Second, 8*time.Second)
			}()
		}

		return nil
	}
	return fmt.Errorf("could not connect to %s", socketPath)
}

func (i *InstanceMonitor) Delete(id, prefix string) error {
	name := id
	if prefix != "" {
		name = fmt.Sprintf("%s:%s", prefix, name)
	}
	i.readyCh <- Status{
		Id:    name,
		Ready: false,
	}

	return nil
}

func (i *InstanceMonitor) PingQga(name string, maxAttempts int, timeout, maxDelay time.Duration) {
	const initialDelay = 500 * time.Millisecond
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		slog.Info("Pinging QGA", "instance", name, "attempt", attempt)
		if req, reqErr := qga.PrepareGuestPingRequest(); reqErr == nil {
			if execResult, chErr := i.qapi.Execute(name, client.Request(*req)); chErr == nil {
				if _, ok := execResult.Get(context.Background(), timeout); !ok {
					if cancelErr := i.qapi.Cancel(req.Id); cancelErr != nil {
						slog.Error("Failed to cancel QGA request", "instance", name, "error", cancelErr)
					}
				} else {
					i.ready[name] = true
					i.readyCh <- Status{
						Id:    name,
						Ready: true,
					}

					slog.Info("QGA is ready", "instance", name)
					return
				}
			}
		}

		if attempt < maxAttempts {
			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	slog.Warn("QGA ping failed", "instance", name, "attempt", maxAttempts)
}

func NewInstanceMonitor() (*InstanceMonitor, error) {
	qapiClient, qapiErr := monitor.NewMonitor()
	if qapiErr != nil {
		return nil, qapiErr
	}

	mon := &InstanceMonitor{
		qapi:    qapiClient,
		ready:   map[string]bool{},
		readyCh: make(chan Status),
	}

	go func() {
		qapiChan := qapiClient.Messages()
		for {
			select {
			case status, ok := <-mon.readyCh:
				if !ok {
					return
				}
				if status.Ready {
					mon.ready[status.Id] = status.Ready
				} else {
					delete(mon.ready, status.Id)
				}
			case msg, ok := <-qapiChan:
				if !ok {
					return
				}
				if msg.Generic != nil {
					var greeting Greeting
					if err := json.Unmarshal(msg.Generic, &greeting); err == nil {
						if req, reqErr := qapi.PrepareQmpCapabilitiesRequest(qapi.QObjQmpCapabilitiesArg{}); reqErr == nil {
							if ch, chErr := qapiClient.Execute(msg.Instance, client.Request(*req)); chErr == nil {
								res, resOk := ch.Get(context.Background(), -1)
								if resOk && res.Return != nil {
									mon.ready[msg.Instance] = true
								}
							}
						}
					}
				}
			}
		}
	}()

	return mon, nil
}

// Close properly shuts down the monitor and releases all resources
func (i *InstanceMonitor) Close() error {
	// Signal the monitor goroutine to stop
	close(i.readyCh)

	// Close all connections via the qapi client
	if i.qapi != nil {
		return i.qapi.Close()
	}

	return nil
}
