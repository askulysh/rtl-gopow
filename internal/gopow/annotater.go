package gopow

import (
	"fmt"
	"image"
	"image/color"
	"bytes"
	"strconv"
	"io/ioutil"
	"math"
	"time"
	"strings"
	"container/heap"

	"github.com/dustin/go-humanize"
	"github.com/golang/freetype"
	log "github.com/sirupsen/logrus"
	"golang.org/x/image/font"

	"github.com/lucasb-eyer/go-colorful"

	"github.com/dhogborg/rtl-gopow/internal/resources"
)

// font configuration
const (
	dpi      float64 = 72
	fontfile string  = "resources/fonts/luxisr.ttf"
	hinting  string  = "none"
	size     float64 = 15
	spacing  float64 = 1.1
)

type Annotator struct {
	image *image.RGBA
	table *TableComplex

	context *freetype.Context
	level   float64
	delta   float64
	jobs    int
}

func NewAnnotator(img *image.RGBA, table *TableComplex,
		  level float64, delta int, jobs int) (*Annotator, error) {

	a := &Annotator{
		image: img,
		table: table,
		level: level,
		delta: float64(delta),
		jobs:  jobs,
	}

	err := a.init()
	if err != nil {
		return nil, err
	}

	return a, nil

}

func (a *Annotator) init() error {

	// load the font
	fontBytes, err := resources.Asset(fontfile)
	if err != nil {
		return err
	}

	luxisr, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return err
	}

	// Initialize the context.
	fg := image.White

	a.context = freetype.NewContext()
	a.context.SetDPI(dpi)
	a.context.SetFont(luxisr)
	a.context.SetFontSize(size)

	a.context.SetClip(a.image.Bounds())
	a.context.SetDst(a.image)
	a.context.SetSrc(fg)

	switch hinting {
	default:
		a.context.SetHinting(font.HintingNone)
	case "full":
		a.context.SetHinting(font.HintingFull)
	}

	return nil
}

func (a *Annotator) Estimate(freq int64) float64 {

	hzpp := (a.table.HzHigh-a.table.HzLow)/float64(a.table.Bins)
	px := int((float64(freq) - a.table.HzLow)/hzpp)
	jumps:=0
	high:= 0.0
	low:= 0.0
	high_cnt := 0
	low_cnt := 0
	min :=  1000.0
	max := -1000.0
	sum := 0.0
	imgSize := a.table.Image().Bounds().Size()
	for i := 1; i < imgSize.Y-1; i++ {
		if a.table.Rows[i].Sample(px) < min {
			min = a.table.Rows[i].Sample(px)
		}
		if a.table.Rows[i].Sample(px) > max {
			max = a.table.Rows[i].Sample(px)
		}
		sum += a.table.Rows[i].Sample(px)
	}
	mean := sum/float64(imgSize.Y)
	lvl := a.level
	if max < lvl || min > lvl {
		lvl = mean
	}
	for i := 1; i < imgSize.Y-1; i++ {
		if a.table.Rows[i].Sample(px) < lvl {
			low +=  a.table.Rows[i].Sample(px)
				low_cnt++
		} else {
			high += a.table.Rows[i].Sample(px)
			high_cnt++
		}
		if math.Abs(a.table.Rows[i].Sample(px)-
		   a.table.Rows[i-1].Sample(px)) > a.delta {
			jumps = jumps + 1
		}
	}
	noise := low/float64(low_cnt)
	SNR:= 0.0
	if low_cnt > 0 {
		SNR = low/float64(low_cnt)
		if high_cnt > 0 {
			SNR = high/float64(high_cnt) - SNR
		}
	}
	snrMax := max - noise
	log.WithFields(log.Fields{
		"lvl": math.Round(lvl),
		"min": math.Round(min),
		"max": math.Round(max),
		"mean": math.Round(mean),
		"jumps":  jumps,
		"est pkts:": 100*imgSize.Y*high_cnt/(high_cnt+low_cnt)/3,
		"high ratio":  100*high_cnt/(high_cnt+low_cnt),
		"noise lvl":  math.Round(noise),
		"SNR": math.Round(SNR),
		"SNRmax": math.Round(snrMax),
		"F":  freq,
	}).Debug("freq")

	return snrMax
}

type Carrier struct {
	snr float64
	freq int64
}

type Carriers []Carrier

func (h Carriers) Less(i, j int) bool {
	return h[i].snr > h[j].snr
}

func (h Carriers) Len() int { return len(h) }

func (h Carriers) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *Carriers) Push(x interface{}) {
	*h = append(*h, x.(Carrier))
}

func (h *Carriers) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func (a *Annotator) FindCarriers() error {

	buff, err := ioutil.ReadFile("freq_list")
	if err != nil {
		return err
	}

	imgSize := a.image.Bounds().Size()
	hzpp := (a.table.HzHigh-a.table.HzLow)/float64(a.table.Bins)

	reqs := make([]int64, 0)
	n := 0

	lines := bytes.Split(buff, []byte("\n"))
	for _, l := range lines {
		arr := strings.Split(string(l), " ")
		freq, _ := strconv.ParseInt(arr[0], 10, 64)
		if freq == 0 {
			break
		}
		if float64(freq) < a.table.HzLow ||
		   float64(freq) > a.table.HzHigh {
			continue
		}
		reqs = append(reqs,freq)
		snr := a.Estimate(freq)
		col, _ := colorful.Hex("#FFFFFF")
		if snr > 10.0 {
			fmt.Printf("%d\n", freq)
			col, _ = colorful.Hex(arr[1])
			if arr[1] != "#FF0000" {
				n++
			}
		}
		px := int((float64(freq) - a.table.HzLow)/hzpp)
		for i := 0; i < imgSize.Y-1; i++ {
			a.image.Set(px, i, col)

		}
	}
        xStart := (int64(a.table.HzLow)/5000 + 1)*5000

	found := &Carriers{}
	heap.Init(found)

	for x := xStart; x < int64(a.table.HzHigh); x += 5000 {
		snr := a.Estimate(x)
		heap.Push(found, Carrier{snr, x})
	}
	for found.Len() > 0 && n < a.jobs {
		c:= heap.Pop(found).(Carrier)
		skip := false
		for _, f := range reqs {
			d := f -c.freq
			if d < 0 {
				d = -d
			}
			if d < 10000 {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		n++
		fmt.Printf("%d\n", c.freq)
		reqs = append(reqs, c.freq)

		col, _ := colorful.Hex("#FF00FF")
		px := int((float64(c.freq) - a.table.HzLow)/hzpp)
		for i := 0; i < imgSize.Y-1; i++ {
			a.image.Set(px, i, col)

		}
	}

	return nil
}

func (a *Annotator) DrawXScale() error {

	log.WithFields(log.Fields{
		"hzHigh": humanize.SI(a.table.HzHigh, "Hz"),
		"hzLow":  humanize.SI(a.table.HzLow, "Hz"),
	}).Debug("annotate X scale")

	// how many samples?
	count := int(math.Floor(float64(a.table.Bins) / float64(100)))

	hzPerLabel := float64(a.table.HzHigh-a.table.HzLow) / float64(count)
	pxPerLabel := int(math.Floor(float64(a.table.Bins) / float64(count)))

	log.WithFields(log.Fields{
		"labels":     count,
		"hzPerLabel": humanize.SI(hzPerLabel, "Hz"),
		"pxPerLabel": pxPerLabel,
	}).Debug("annotate X scale")

	for si := 0; si < count; si++ {

		hz := a.table.HzLow + (float64(si) * hzPerLabel)
		px := si * pxPerLabel

		fract, suffix := humanize.ComputeSI(hz)
		str := fmt.Sprintf("%0.2f %sHz", fract, suffix)

		// draw a guideline on the exact frequency
		for i := 0; i < 30; i++ {
			a.image.Set(px, i, image.White)
		}

		// draw the text
		pt := freetype.Pt(px+5, 17)
		_, _ = a.context.DrawString(str, pt)

	}

	xStart := (math.Floor(a.table.HzLow/10000) + 1)*10000
	hzpp := (a.table.HzHigh-a.table.HzLow)/float64(a.table.Bins)
	log.WithFields(log.Fields{
		"xStart": xStart,
		"hzpp": hzpp,
	}).Debug("annotate X scale2")

	for x := xStart; x < a.table.HzHigh; x += 10000 {
		px := int((x - a.table.HzLow)/hzpp)
		l := 10
		if int(x) % 100000 == 0 {
			l= 20
		}
		for i := 0; i < l; i++ {
			a.image.Set(px, i, color.RGBA{255, 0, 0, 255})
		}
	}

	a.FindCarriers()

	return nil
}

func (a *Annotator) DrawYScale() error {

	log.WithFields(log.Fields{
		"timestart": a.table.TimeStart.String(),
		"timeend":   a.table.TimeEnd.String(),
	}).Debug("annotate Y scale")

	start, end := a.table.TimeStart, a.table.TimeEnd

	// how many samples?
	count := int(math.Floor(float64(a.table.Integrations) / float64(100)))

	uStart := start.Unix()
	uEnd := end.Unix()

	secsPerLabel := int(math.Floor(float64(uEnd-uStart) / float64(count)))
	pxPerLabel := int(math.Floor(float64(a.table.Integrations) / float64(count)))

	log.WithFields(log.Fields{
		"labels":       count,
		"secsPerLabel": secsPerLabel,
		"pxPerLabel":   pxPerLabel,
	}).Debug("annotate Y scale")

	for si := 0; si < count; si++ {

		secs := time.Duration(secsPerLabel * si * int(time.Second))
		px := si * pxPerLabel

		var str string = ""

		if si == 0 {
			str = start.String()
		} else {
			point := start.Add(secs)
			str = point.Format("15:04:05")
		}

		// draw a guideline on the exact time
		for i := 0; i < 75; i++ {
			a.image.Set(i, px, image.White)
		}

		// draw the text, 3 px margin to the line
		pt := freetype.Pt(3, px-3)
		_, _ = a.context.DrawString(str, pt)

	}

	return nil

}

func (a *Annotator) DrawInfoBox() error {

	tStart, tEnd := a.table.TimeStart, a.table.TimeEnd
	// tDuration := humanize.RelTime(*tStart, *tEnd, "", "")
	tPixel := (tEnd.Unix() - tStart.Unix()) / int64(a.table.Integrations)

	fStart, fEnd := a.table.HzLow, a.table.HzHigh
	fBandwidth := fEnd - fStart
	fPixel := fBandwidth / float64(a.table.Bins)

	perPixel := fmt.Sprintf("%s x %d seconds", a.humanHz(fPixel), tPixel)

	// positioning
	imgSize := a.table.Image().Bounds().Size()
	top, left := imgSize.Y-75, 3

	strings := []string{
		"Scan start: " + tStart.String(),
		"Scan end: " + tEnd.String(),
		// "Scan duration: " + tDuration,
		fmt.Sprintf("Band: %s to %s", a.humanHz(fStart), a.humanHz(fEnd)),
		fmt.Sprintf("Bandwidth: %s", a.humanHz(fBandwidth)),
		"1 pixel = " + perPixel,
	}

	// drawing
	pt := freetype.Pt(left, top)
	for _, s := range strings {
		_, _ = a.context.DrawString(s, pt)
		pt.Y += a.context.PointToFixed(size * spacing)
	}

	return nil
}

func (a *Annotator) humanHz(hz float64) string {
	fpxSI, fpxSuffix := humanize.ComputeSI(hz)
	return fmt.Sprintf("%0.2f %sHz", fpxSI, fpxSuffix)
}
