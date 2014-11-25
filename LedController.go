package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/lucasb-eyer/go-colorful"
	"github.com/ninjasphere/go-ninja/api"
	"github.com/ninjasphere/go-ninja/config"
	"github.com/ninjasphere/go-ninja/logger"
	"github.com/ninjasphere/go-ninja/model"
	"github.com/ninjasphere/goserial"
	ledmodel "github.com/ninjasphere/sphere-go-led-controller/model"
	"github.com/ninjasphere/sphere-go-led-controller/ui"
	"github.com/ninjasphere/sphere-go-led-controller/util"
)

var log = logger.GetLogger("sphere-go-led-controller")

var fps Tick = Tick{
	name: "Pane FPS",
}

type LedController struct {
	controlEnabled bool
	controlLayout  *ui.PaneLayout
	pairingLayout  *ui.PairingLayout
	conn           *ninja.Connection
	serial         io.ReadWriteCloser
	waiting        chan bool
}

func GetLEDConnection(baudRate int) (io.ReadWriteCloser, error) {

	log.Debugf("Resetting LED Matrix")
	cmd := exec.Command("/usr/local/bin/reset-led-matrix")
	output, err := cmd.Output()
	log.Debugf("Output from reset: %s", output)

	c := &serial.Config{Name: "/dev/tty.ledmatrix", Baud: baudRate}
	s, err := serial.OpenPort(c)
	if err != nil {
		return nil, err
	}

	// Now we wait for the init string
	buf := make([]byte, 16)
	_, err = s.Read(buf)
	if err != nil {
		log.Fatalf("Failed to read initialisation string from led matrix : %s", err)
	}
	if string(buf[0:3]) != "LED" {
		log.Infof("Expected init string 'LED', got '%s'.", buf)
		s.Close()
		return nil, fmt.Errorf("Bad init string..")
	}

	log.Debugf("Read init string from LED Matrix: %s", buf)

	return s, nil
}

const baudRate = 115200

func NewLedController(conn *ninja.Connection) (*LedController, error) {

	s, err := GetLEDConnection(baudRate * 2)

	if err != nil {
		log.Warningf("Failed to connect to LED using baud rate: %d, trying %d", baudRate*2, baudRate)
		s, err = GetLEDConnection(baudRate)
		if err != nil {
			log.Fatalf("Failed to connect to LED display: %s", err)
		}
	}
	// Send a blank image to the led matrix
	util.WriteLEDMatrix(image.NewRGBA(image.Rect(0, 0, 16, 16)), s)

	controller := &LedController{
		conn:          conn,
		pairingLayout: ui.NewPairingLayout(conn),
		serial:        s,
		waiting:       make(chan bool),
	}

	conn.MustExportService(controller, "$node/"+config.Serial()+"/led-controller", &model.ServiceAnnouncement{
		Schema: "/service/led-controller",
	})

	return controller, nil
}

func (c *LedController) start(enableControl bool) {
	c.controlEnabled = enableControl

	frameWritten := make(chan bool)

	go func() {
		fps.start()

		for {
			fps.tick()

			if c.controlEnabled {

				if c.controlLayout == nil {

					log.Infof("Enabling layout... clearing LED")

					util.WriteLEDMatrix(image.NewRGBA(image.Rect(0, 0, 16, 16)), c.serial)

					c.controlLayout = getPaneLayout(c.conn)
					log.Infof("Finished control layout")
				}

				image, wake, err := c.controlLayout.Render()
				if err != nil {
					log.Fatalf("Unable to render()", err)
				}

				go func() {
					util.WriteLEDMatrix(image, c.serial)
					frameWritten <- true
				}()

				select {
				case <-frameWritten:
					// All good.
				case <-time.After(10 * time.Second):
					log.Infof("Timeout writing to LED matrix. Quitting.")
					os.Exit(1)
					// Timed out writing to the led matrix. For now. Boot!
					//cmd := exec.Command("reboot")
					//output, err := cmd.Output()

					//log.Debugf("Output from reboot: %s err: %s", output, err)
				}

				if wake != nil {
					log.Infof("Waiting as the UI is asleep")
					select {
					case <-wake:
						log.Infof("UI woke up!")
					case <-c.waiting:
						log.Infof("Got a command from rpc...")
					}
				}

			} else {

				image, err := c.pairingLayout.Render()
				if err != nil {
					log.Fatalf("Unable to render()", err)
				}
				util.WriteLEDMatrix(image, c.serial)

			}
		}
	}()
}

func (c *LedController) EnableControl() error {
	c.controlEnabled = true
	c.gotCommand()
	return nil
}

func (c *LedController) DisableControl() error {
	c.controlEnabled = false
	c.gotCommand()
	return nil
}

type PairingCodeRequest struct {
	Code        string `json:"code"`
	DisplayTime int    `json:"displayTime"`
}

func (c *LedController) DisplayPairingCode(req *PairingCodeRequest) error {
	c.controlEnabled = false
	c.pairingLayout.ShowCode(req.Code)
	c.gotCommand()
	return nil
}

type ColorRequest struct {
	Color       string `json:"color"`
	DisplayTime int    `json:"displayTime"`
}

func (c *LedController) DisplayColor(req *ColorRequest) error {
	col, err := colorful.Hex(req.Color)

	if err != nil {
		return err
	}

	c.controlEnabled = false
	c.pairingLayout.ShowColor(col)
	c.gotCommand()
	return nil
}

type IconRequest struct {
	Icon        string `json:"icon"`
	DisplayTime int    `json:"displayTime"`
}

func (c *LedController) DisplayIcon(req *IconRequest) error {
	c.controlEnabled = false
	c.pairingLayout.ShowIcon(req.Icon)
	c.gotCommand()
	return nil
}

func (c *LedController) DisplayResetMode(m *ledmodel.ResetMode) error {
	c.controlEnabled = false
	fade := m.Duration > 0 && !m.Hold
	loading := false
	var col color.Color
	switch m.Mode {
	case "reboot":
		col, _ = colorful.Hex("#00FF00")
	case "reset-userdata":
		col, _ = colorful.Hex("#FFFF00")
	case "reset-root":
		col, _ = colorful.Hex("#FF0000")
	default:
		loading = true
	}

	if loading {
		c.pairingLayout.ShowIcon("loading.gif")
	} else if fade {
		c.pairingLayout.ShowFadingShrinkingColor(col, m.Duration)
	} else {
		c.pairingLayout.ShowColor(col)
	}

	c.gotCommand()
	return nil
}

func (c *LedController) gotCommand() {
	select {
	case c.waiting <- true:
	default:
	}
}

// Load from a config file instead...
func getPaneLayout(conn *ninja.Connection) *ui.PaneLayout {
	layout, wake := ui.NewPaneLayout(false)

	mediaPane := ui.NewMediaPane(&ui.MediaPaneImages{
		Volume: "images/media-volume-speaker.gif",
		Mute:   "images/media-volume-mute.png",
		Play:   "images/media-play.png",
		Pause:  "images/media-pause.png",
		Stop:   "images/media-stop.png",
		Next:   "images/media-next.png",
	}, conn)
	layout.AddPane(mediaPane)

	if len(os.Getenv("CERTIFICATION")) > 0 {
		layout.AddPane(ui.NewCertPane(conn.GetMqttClient()))
	} else {
		//layout.AddPane(ui.NewTextScrollPane("Exit Music (For A Film)"))

		heaterPane := ui.NewOnOffPane("images/heater-off.png", "images/heater-on.gif", func(state bool) {
			log.Debugf("Heater state: %t", state)
		}, conn, "heater")
		layout.AddPane(heaterPane)
	}

	lightPane := ui.NewLightPane("images/light-off.png", "images/light-on.png", func(state bool) {
		log.Debugf("Light on-off state: %t", state)
	}, func(state float64) {
		log.Debugf("Light color state: %f", state)
	}, conn)
	layout.AddPane(lightPane)

	fanPane := ui.NewOnOffPane("images/fan-off.png", "images/fan-on.gif", func(state bool) {
		log.Debugf("Fan state: %t", state)
	}, conn, "fan")

	layout.AddPane(fanPane)

	go func() {
		<-wake
	}()

	go layout.Wake()

	return layout
}

type Tick struct {
	count int
	name  string
}

func (t *Tick) tick() {
	t.count++
}

func (t *Tick) start() {
	go func() {
		for {
			time.Sleep(time.Second)
			log.Debugf("%s - %d", t.name, t.count)
			t.count = 0
		}
	}()
}
