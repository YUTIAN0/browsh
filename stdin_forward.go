package main

import (
	"fmt"
	"strings"
	"time"
	"os"
	"os/exec"
	"math"
	"github.com/tombh/termbox-go"
)

// Import the xzoom C code that creates an X window that zooms
// and pans the desktop.
// It's written in C because it borrows from the original xzoom
// program: http://git.r-36.net/xzoom/
// NB: The following comments are parsed by `go build` ...

// #cgo LDFLAGS: -lXext -lX11 -lXt
// #include "xzoom/xzoom.h"
import "C"

var logfile = "./input.log"
var current string
var curev termbox.Event
var lastMouseButton string
var desktopWidth float32 = 1600
var desktopHeight float32 = 1200
var desktopXFloat float32
var desktopYFloat float32
var roundedDesktopX int
var roundedDesktopY int

// Dimensions of hiptext output
var hipWidth int
var hipHeight int

var panNeedsSetup bool
var panCachedXOffset float32
var panCachedYOffset float32

func initialise() {
	tErr := os.Truncate(logfile, 0)
	if tErr != nil {
		panic(tErr)
	}
	log("Starting...")
	calculateHipDimensions()
}

// Hiptext needs to render the aspect ratio faithfully. So firstly it tries to fill
// the terminal as much as it can. And secondly it treats a row as representing twice
// as much as a column - thus why there are some multiplications/divisions by 2.
func calculateHipDimensions() {
	_tw, _th := termbox.Size()
	tw := float32(_tw)
	th := float32(_th * 2)
	ratio := desktopWidth / desktopHeight
	bestHeight := min(th, (tw / ratio))
  bestWidth := min(tw, (bestHeight * ratio))
	// Not sure why the +1 and -1 are needed, but they are.
  hipWidth = roundToInt(bestWidth) + 1
  hipHeight = roundToInt(bestHeight / 2) - 1
	log(fmt.Sprintf("Term dimensions: W: %d, H: %d", _tw, _th))
	log(fmt.Sprintf("Hiptext dimensions: W: %d, H: %d", hipWidth, hipHeight))
}

func min(a float32, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func log(msg string) {
	f, oErr := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if oErr != nil {
		panic(oErr)
	}
	defer f.Close()

	msg = msg + "\n"
	if _, wErr := f.WriteString(msg); wErr != nil {
		panic(wErr)
	}
}

// Issue an xdotool command to simulate mouse and keyboard input
func xdotool(args ...string) {
	log(strings.Join(args, " "))
	if args[0] == "noop" {
		return
	}
	if err := exec.Command("xdotool", args...).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func roundToInt(value32 float32) int {
	var rounded float64
	value := float64(value32)
	if value < 0 {
		rounded = math.Ceil(value - 0.5)
	}
	rounded = math.Floor(value + 0.5)
	return int(rounded)
}

// Whether the current input event includes a depressed CTRL key.
func ctrlPressed() bool {
	return curev.Mod&termbox.ModCtrl != 0
}

// Whether the mouse is moving
func mouseMotion() bool {
	return curev.Mod&termbox.ModMotion != 0
}

// Convert Termbox symbols to xdotool arguments
func mouseButtonStr(k termbox.Key) []string {
	switch k {
	case termbox.MouseLeft:
		lastMouseButton = "1"
		return []string{"mousedown", lastMouseButton}
	case termbox.MouseMiddle:
		lastMouseButton = "2"
		return []string{"mousedown", lastMouseButton}
	case termbox.MouseRight:
		lastMouseButton = "3"
		return []string{"mousedown", lastMouseButton}
	case termbox.MouseRelease:
		return []string{"mouseup", lastMouseButton}
	case termbox.MouseWheelUp:
		if ctrlPressed() {
			C.magx++
			C.magy++
			return []string{"noop"}
		}
		return []string{"click", "4"}
	case termbox.MouseWheelDown:
		if ctrlPressed() {
			if C.magx > 1 {
				C.magx--
				C.magy--
	    }
			return []string{"noop"}
		}
		return []string{"click", "5"}
	}
	return []string{""}
}

// Auxillary data. Whether the mouse was moving or a mod key like CTRL
// is being pressed at the same time.
func modStr(m termbox.Modifier) string {
	var out []string
	if m&termbox.ModAlt != 0 {
		out = append(out, "Alt")
	}
	if m&termbox.ModMotion != 0 {
		out = append(out, "Motion")
	}
	// Depends on this PR: https://github.com/nsf/termbox-go/pull/126
	if m&termbox.ModCtrl != 0 {
		out = append(out, "Ctrl")
	}

	return strings.Join(out, " ")
}

func mouseEvent() {
	setCurrentDesktopCoords()
	// Always move the mouse first so that button presses are correct. This is because we're not constantly
	// updating the mouse position, *unless* a drag event is happening. This saves bandwidth. Also, mouse
	// movement isn't supported on all terminals.
	xdotool("mousemove", fmt.Sprintf("%d", roundedDesktopX), fmt.Sprintf("%d", roundedDesktopY))

	log(
		fmt.Sprintf(
			"EventMouse: x: %d, y: %d, b: %s, mod: %s",
			curev.MouseX, curev.MouseY, mouseButtonStr(curev.Key), modStr(curev.Mod)))

	if ctrlPressed() && mouseMotion() && lastMouseButton == "1" {
		C.pan = 1
		if panNeedsSetup == true {
			panCachedXOffset = float32(C.xgrab)
			panCachedYOffset = float32(C.ygrab)
		}
		panNeedsSetup = false
	} else {
		panNeedsSetup = true
		C.pan = 0
		if !ctrlPressed() {
			xdotool(mouseButtonStr(curev.Key)...)
		}
	}
}

// Convert terminal coords into desktop coords
func setCurrentDesktopCoords() {
	var xOffset float32
	var yOffset float32
	hipWidthFloat := float32(hipWidth)
	hipHeightFloat := float32(hipHeight)
	eventX := float32(curev.MouseX)
	eventY := float32(curev.MouseY)
	width := float32(C.width[C.SRC])
	height := float32(C.height[C.SRC])
	if C.pan == 1 {
		// When panning starts we want to do it all within the same viewport.
		// Without the caching here, then the viewport would change for each
		// mouse movement and panning becomes overly sensitive.
		xOffset = panCachedXOffset
		yOffset = panCachedYOffset
	} else {
		xOffset = float32(C.xgrab)
		yOffset = float32(C.ygrab)
	}
	desktopXFloat = (eventX * (width / hipWidthFloat)) + xOffset
	desktopYFloat = (eventY * (height / hipHeightFloat)) + yOffset
	log(
		fmt.Sprintf(
			"setCurrentDesktopCoords: tw: %d, th: %d, dx: %d, dy: %d, mag: %d",
			hipHeightFloat, hipWidthFloat, desktopXFloat, desktopYFloat, C.magx))
	roundedDesktopX = roundToInt(desktopXFloat)
	roundedDesktopY = roundToInt(desktopYFloat)
}

// Convert a keyboard event into an xdotool command
// See: http://wiki.linuxquestions.org/wiki/List_of_Keysyms_Recognised_by_Xmodmap
func keyEvent() {
	var key string
	var command string
	log(fmt.Sprintf("EventKey: k: %d, c: %c, mod: %s", curev.Key, curev.Ch, modStr(curev.Mod)))

	switch curev.Key {
	case termbox.KeyEnter:
	    key = "Return"
	case termbox.KeyBackspace, termbox.KeyBackspace2:
	    key = "BackSpace"
	case termbox.KeySpace:
	    key = "Space"
	case termbox.KeyF1:
		key = "F1"
	case termbox.KeyF2:
		key = "F2"
	case termbox.KeyF3:
		key = "F3"
	case termbox.KeyF4:
		key = "F4"
	case termbox.KeyF5:
		key = "F5"
	case termbox.KeyF6:
		key = "F6"
	case termbox.KeyF7:
		key = "F7"
	case termbox.KeyF8:
		key = "F8"
	case termbox.KeyF9:
		key = "F9"
	case termbox.KeyF10:
		key = "F10"
	case termbox.KeyF11:
		key = "F11"
	case termbox.KeyF12:
		key = "F12"
	case termbox.KeyInsert:
		key = "Insert"
	case termbox.KeyDelete:
		key = "Delete"
	case termbox.KeyHome:
		key = "Home"
	case termbox.KeyEnd:
		key = "End"
	case termbox.KeyPgup:
		key = "Prior"
	case termbox.KeyPgdn:
		key = "Next"
	case termbox.KeyArrowUp:
		key = "Up"
	case termbox.KeyArrowDown:
		key = "Down"
	case termbox.KeyArrowLeft:
		key = "Left"
	case termbox.KeyArrowRight:
		key = "Right"
	case termbox.KeyCtrlL:
		key = "ctrl+l"
	}

	if curev.Key == 0 {
		key = fmt.Sprintf("%c", curev.Ch)
		command = "type"
	} else {
		command = "key"
	}

	// What is this? It always appears when the program starts :/
	badkey := fmt.Sprintf("%s", curev.Ch) == "%!s(int32=0)" && curev.Key == 0

	if key == "" || badkey {
		log(fmt.Sprintf("No key found for keycode: %d"))
		return
	}

	xdotool(command, key)
}

func parseInput() {
	switch curev.Type {
	case termbox.EventKey:
		keyEvent()
	case termbox.EventMouse:
		mouseEvent()
	case termbox.EventNone:
		log("EventNone")
	}
}

// a channel to tell it to stop
var stopchan = make(chan struct{})
// a channel to signal that it's stopped
var stoppedchan = make(chan struct{})

func xzoomBackground(){
	go func(){ // work in background
	  // close the stoppedchan when this func
	  // exits
	  defer close(stoppedchan)
	  // TODO: do setup work
	  defer func(){
	    // TODO: do teardown work
	  }()
	  for {
	    select {
	      default:
	        C.loop()
					time.Sleep(40 * time.Millisecond) // 25fps
	      case <-stopchan:
	        // stop
	        return
	    }
	  }
	}()
}

func main() {
	C.xzoom_init()
	xzoomBackground()

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer func() {
		termbox.Close()
		close(stopchan)
		<-stoppedchan
	}()
	termbox.SetInputMode(termbox.InputMouse)
	initialise()
	parseInput()

	data := make([]byte, 0, 64)
	for {
		if cap(data)-len(data) < 32 {
			newdata := make([]byte, len(data), len(data)+32)
			copy(newdata, data)
			data = newdata
		}
		beg := len(data)
		d := data[beg : beg+32]
		switch ev := termbox.PollRawEvent(d); ev.Type {
		case termbox.EventRaw:
			data = data[:beg+ev.N]
			current = fmt.Sprintf("%q", data)
			for {
				ev := termbox.ParseEvent(data)
				if ev.N == 0 {
					break
				}
				curev = ev
				copy(data, data[curev.N:])
				data = data[:len(data)-curev.N]
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		parseInput()
	}
}
