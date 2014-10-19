package ui

import (
	"image"
	"strings"
	"time"

	"github.com/ninjasphere/go-gestic"
	"github.com/ninjasphere/go-ninja/api"
	"github.com/ninjasphere/go-ninja/logger"
)

type OnOffPane struct {
	log  *logger.Logger
	conn *ninja.Connection

	devices []*ninja.ServiceClient

	state         bool
	onStateChange func(bool)

	onImage  *Image
	offImage *Image

	ignoringGestures bool
}

func NewOnOffPane(offImage string, onImage string, onStateChange func(bool), conn *ninja.Connection, thingType string) *OnOffPane {

	log := logger.GetLogger("OnOffPane")

	pane := &OnOffPane{
		onImage:       loadImage(onImage),
		offImage:      loadImage(offImage),
		onStateChange: onStateChange,
		log:           log,
		devices:       make([]*ninja.ServiceClient, 0),
		conn:          conn,
	}

	getChannelServicesContinuous(thingType, "on-off", func(devices []*ninja.ServiceClient, err error) {
		if err != nil {
			log.Infof("Failed to update devices: %s", err)
		} else {
			log.Infof("Pane got %d on/off devices", len(devices))
			pane.devices = devices
		}
	})

	return pane
}

func (p *OnOffPane) Gesture(gesture *gestic.GestureData) {
	if p.ignoringGestures {
		return
	}

	if strings.Contains(gesture.Touch.Name(), "Tap") {
		p.log.Infof("Tap!")

		p.ignoringGestures = true

		go func() {
			time.Sleep(time.Millisecond * 250)
			p.ignoringGestures = false
		}()

		p.SetState(!p.state)
	}
}

func (p *OnOffPane) SetState(state bool) {
	p.state = state
	for _, device := range p.devices {
		if state {
			device.Call("turnOn", nil, nil, 0)
		} else {
			device.Call("turnOff", nil, nil, 0)
		}
	}
	p.onStateChange(state)
}

func (p *OnOffPane) Render() (*image.RGBA, error) {
	if p.state {
		return p.onImage.GetNextFrame(), nil
	}
	return p.offImage.GetNextFrame(), nil
}

func (p *OnOffPane) IsDirty() bool {
	return true
}
