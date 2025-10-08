package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/q-controller/qapi-client/src/client"
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
	qapi    *client.Monitor
	ready   map[string]bool
	readyCh chan Status
}

func (i *InstanceMonitor) Execute(name string, request client.Request) (<-chan client.Result, error) {
	ready, exists := i.ready[name]
	if exists && ready {
		return i.qapi.Execute(name, request)
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
			slog.Error("Could not add instance", "instance", name, "error", addErr)
			time.Sleep(time.Duration(intervalMs) * time.Millisecond)
			continue
		}

		slog.Info("Added new instance", "instance", name)

		if prefix == PREFIX_QGA {
			go func() {
				if req, reqErr := qga.PrepareGuestPingRequest(); reqErr == nil {
					if ch, chErr := i.qapi.Execute(name, client.Request(*req)); chErr == nil {
						<-ch
						i.readyCh <- Status{
							Id:    name,
							Ready: true,
						}
					}
				}
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

func NewInstanceMonitor() (*InstanceMonitor, error) {
	qapiClient, qapiErr := client.NewMonitor()
	if qapiErr != nil {
		return nil, qapiErr
	}

	mon := &InstanceMonitor{
		qapi:    qapiClient,
		ready:   map[string]bool{},
		readyCh: make(chan Status),
	}

	go func() {
		qapiChan := qapiClient.Start()
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
								res := <-ch
								if res.Raw.Return != nil {
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
