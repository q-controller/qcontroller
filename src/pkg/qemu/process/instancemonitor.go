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
	"github.com/q-controller/qcontroller/src/pkg/utils"
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
	if err := utils.WaitForFileCreation(socketPath, 60*time.Second); err != nil {
		return err
	}
	if addErr := i.qapi.Add(name, socketPath); addErr != nil {
		return <-addErr
	}
	return nil
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
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		requests := make(map[string]string)
		qapiChan := qapiClient.Messages()
		for {
			select {
			case <-timer.C:
				for reqId, instance := range requests {
					if req, reqErr := qga.PrepareGuestPingRequest(); reqErr == nil {
						if _, chErr := mon.qapi.Execute(instance, client.Request(*req)); chErr == nil {
							requests[req.Id] = instance
						}
						delete(requests, reqId)
					}
				}
			case monitorEvent := <-qapiChan:
				if monitorEvent.InstanceMessage != nil {
					if monitorEvent.InstanceMessage.InstanceMessageType == monitor.InstanceMessageAdd {
						if req, reqErr := qga.PrepareGuestPingRequest(); reqErr == nil {
							if _, chErr := mon.qapi.Execute(monitorEvent.InstanceMessage.Instance, client.Request(*req)); chErr == nil {
								requests[req.Id] = monitorEvent.InstanceMessage.Instance
							}
						}
					}
				}
				if monitorEvent.Message != nil {
					msg := *monitorEvent.Message

					if msg.Type == monitor.MessageGeneric && msg.Generic == nil {
						mon.ready[msg.Instance] = false
						delete(mon.ready, msg.Instance)
						continue
					}
					if msg.Generic != nil {
						var greeting Greeting
						if err := json.Unmarshal(msg.Generic, &greeting); err == nil {
							// Verify that the unmarshaled greeting has the expected QMP structure
							if greeting.QMP.Version.Qemu.Major != 0 || greeting.QMP.Version.Qemu.Minor != 0 || greeting.QMP.Version.Qemu.Micro != 0 {
								if req, reqErr := qapi.PrepareQmpCapabilitiesRequest(qapi.QObjQmpCapabilitiesArg{}); reqErr == nil {
									if ch, chErr := qapiClient.Execute(msg.Instance, client.Request(*req)); chErr == nil {
										res, resOk := ch.Get(context.Background(), -1)
										if resOk && res.Return != nil {
											mon.ready[msg.Instance] = true
											slog.Info("QMP is ready", "instance", msg.Instance)
											for reqId, instance := range requests {
												if instance == msg.Instance {
													delete(requests, reqId)
												}
											}
											continue
										}
									}
								}
							}
						}
						var result client.QAPIResult
						if err := json.Unmarshal(msg.Generic, &result); err == nil {
							if reqId, reqIdOk := requests[result.Id]; reqIdOk && result.Error == nil {
								mon.ready[reqId] = true
								slog.Info("QGA is ready", "instance", reqId)
								delete(requests, result.Id)
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
