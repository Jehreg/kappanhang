package main

import (
	"fmt"
	"math"
	"time"
)

const civAddress = 0xa4
const sReadInterval = time.Second

type civOperatingMode struct {
	name string
	code byte
}

var civOperatingModes = []civOperatingMode{
	{name: "LSB", code: 0x00},
	{name: "USB", code: 0x01},
	{name: "AM", code: 0x02},
	{name: "CW", code: 0x03},
	{name: "RTTY", code: 0x04},
	{name: "FM", code: 0x05},
	{name: "WFM", code: 0x06},
	{name: "CW-R", code: 0x07},
	{name: "RTTY-R", code: 0x08},
	{name: "DV", code: 0x17},
}

type civFilter struct {
	name string
	code byte
}

var civFilters = []civFilter{
	{name: "FIL1", code: 0x01},
	{name: "FIL2", code: 0x02},
	{name: "FIL3", code: 0x03},
}

type civBand struct {
	freqFrom uint
	freqTo   uint
	freq     uint
}

var civBands = []civBand{
	{freqFrom: 1800000, freqTo: 1999999},     // 1.9
	{freqFrom: 3400000, freqTo: 4099999},     // 3.5
	{freqFrom: 6900000, freqTo: 7499999},     // 7
	{freqFrom: 9900000, freqTo: 10499999},    // 10
	{freqFrom: 13900000, freqTo: 14499999},   // 14
	{freqFrom: 17900000, freqTo: 18499999},   // 18
	{freqFrom: 20900000, freqTo: 21499999},   // 21
	{freqFrom: 24400000, freqTo: 25099999},   // 24
	{freqFrom: 28000000, freqTo: 29999999},   // 28
	{freqFrom: 50000000, freqTo: 54000000},   // 50
	{freqFrom: 74800000, freqTo: 107999999},  // WFM
	{freqFrom: 108000000, freqTo: 136999999}, // AIR
	{freqFrom: 144000000, freqTo: 148000000}, // 144
	{freqFrom: 420000000, freqTo: 450000000}, // 430
	{freqFrom: 0, freqTo: 0},                 // GENE
}

type civControlStruct struct {
	st              *serialStream
	deinitNeeded    chan bool
	deinitFinished  chan bool
	resetSReadTimer chan bool

	state struct {
		freq             uint
		ptt              bool
		tune             bool
		pwrPercent       int
		operatingModeIdx int
		filterIdx        int
		dataMode         bool
		bandIdx          int
		bandChanging     bool
		preamp           int
		tsValue          byte
		ts               uint
	}
}

var civControl *civControlStruct

func (s *civControlStruct) decode(d []byte) {
	if len(d) < 6 || d[0] != 0xfe || d[1] != 0xfe || d[len(d)-1] != 0xfd {
		return
	}

	payload := d[5 : len(d)-1]

	switch d[4] {
	case 0x00:
		s.decodeFreq(payload)
	case 0x01:
		s.decodeMode(payload)
	case 0x03:
		s.decodeFreq(payload)
	case 0x04:
		s.decodeMode(payload)
	case 0x10:
		s.decodeTS(payload)
	case 0x1a:
		s.decodeDataModeAndOVF(payload)
	case 0x14:
		s.decodePower(payload)
	case 0x1c:
		s.decodeTransmitStatus(payload)
	case 0x15:
		s.decodeVdAndS(payload)
	case 0x16:
		s.decodePreamp(payload)
	}
}

func (s *civControlStruct) decodeFreq(d []byte) {
	var f uint
	var pos int
	for _, v := range d {
		s1 := v & 0x0f
		s2 := v >> 4
		f += uint(s1) * uint(math.Pow(10, float64(pos)))
		pos++
		f += uint(s2) * uint(math.Pow(10, float64(pos)))
		pos++
	}
	if f == 0 {
		return
	}
	s.state.freq = f
	statusLog.reportFrequency(s.state.freq)

	s.state.bandIdx = len(civBands) - 1 // Set the band idx to GENE by default.
	for i := range civBands {
		if s.state.freq >= civBands[i].freqFrom && s.state.freq <= civBands[i].freqTo {
			s.state.bandIdx = i
			civBands[s.state.bandIdx].freq = s.state.freq
			break
		}
	}
}

func (s *civControlStruct) decodeFilterValueToFilterIdx(v byte) int {
	for i := range civFilters {
		if civFilters[i].code == v {
			return i
		}
	}
	return -1
}

func (s *civControlStruct) decodeMode(d []byte) {
	if len(d) < 1 {
		return
	}

	var mode string
	for i := range civOperatingModes {
		if civOperatingModes[i].code == d[0] {
			s.state.operatingModeIdx = i
			mode = civOperatingModes[i].name
			break
		}
	}

	var filter string
	if len(d) > 1 {
		s.state.filterIdx = s.decodeFilterValueToFilterIdx(d[1])
		filter = civFilters[s.state.filterIdx].name
	}
	statusLog.reportMode(mode, filter)

	// The transceiver does not send the data mode setting automatically.
	_ = s.getDataMode()
}

func (s *civControlStruct) decodeTS(d []byte) {
	if len(d) < 1 {
		return
	}

	s.state.tsValue = d[0]

	switch s.state.tsValue {
	default:
		s.state.ts = 1
	case 1:
		s.state.ts = 100
	case 2:
		s.state.ts = 500
	case 3:
		s.state.ts = 1000
	case 4:
		s.state.ts = 5000
	case 5:
		s.state.ts = 6250
	case 6:
		s.state.ts = 8330
	case 7:
		s.state.ts = 9000
	case 8:
		s.state.ts = 10000
	case 9:
		s.state.ts = 12500
	case 10:
		s.state.ts = 20000
	case 11:
		s.state.ts = 25000
	case 12:
		s.state.ts = 50000
	case 13:
		s.state.ts = 100000
	}
	statusLog.reportTS(s.state.ts)
}

func (s *civControlStruct) decodeDataModeAndOVF(d []byte) {
	if len(d) < 2 {
		return
	}

	switch d[0] {
	case 0x06:
		if len(d) < 3 {
			return
		}
		var dataMode string
		var filter string
		if d[1] == 1 {
			dataMode = "-D"
			s.state.dataMode = true
			s.state.filterIdx = s.decodeFilterValueToFilterIdx(d[2])
			filter = civFilters[s.state.filterIdx].name
		} else {
			s.state.dataMode = false
		}

		statusLog.reportDataMode(dataMode, filter)
	case 0x09:
		if d[1] != 0 {
			statusLog.reportOVF(true)
		} else {
			statusLog.reportOVF(false)
		}
	}
}

func (s *civControlStruct) decodePower(d []byte) {
	if len(d) < 3 || d[0] != 0x0a {
		return
	}

	hex := uint16(d[1])<<8 | uint16(d[2])
	s.state.pwrPercent = int(math.Round((float64(hex) / 0x0255) * 100))

	statusLog.reportTxPower(s.state.pwrPercent)
}

func (s *civControlStruct) decodeTransmitStatus(d []byte) {
	if len(d) < 2 {
		return
	}

	switch d[0] {
	case 0:
		if d[1] == 1 {
			s.state.ptt = true
		} else {
			if s.state.ptt { // PTT released?
				s.state.ptt = false
				_ = s.getVd()
			}
		}
	case 1:
		if d[1] == 2 {
			s.state.tune = true

			// The transceiver does not send the tune state after it's finished.
			time.AfterFunc(time.Second, func() {
				_ = s.getTransmitStatus()
			})
		} else {
			if s.state.tune { // Tune finished?
				s.state.tune = false
				_ = s.getVd()
			}
		}
	}
	statusLog.reportPTT(s.state.ptt, s.state.tune)
}

func (s *civControlStruct) decodeVdAndS(d []byte) {
	if len(d) < 3 {
		return
	}

	switch d[0] {
	case 0x02:
		sValue := (int(math.Round(((float64(int(d[1])<<8) + float64(d[2])) / 0x0241) * 18)))
		sStr := "S"
		if sValue <= 9 {
			sStr += fmt.Sprint(sValue)
		} else {
			sStr += "9+"

			if sValue > 18 {
				sStr += "60"
			} else {
				dB := (float64((sValue - 9)) / 9) * 60
				sStr += fmt.Sprint(int(math.Round(dB/10) * 10))
			}
		}
		statusLog.reportS(sStr)
	case 0x15:
		statusLog.reportVd(((float64(int(d[1])<<8) + float64(d[2])) / 0x0241) * 16)
	}
}

func (s *civControlStruct) decodePreamp(d []byte) {
	if len(d) < 2 {
		return
	}

	switch d[0] {
	case 0x02:
		s.state.preamp = int(d[1])
		statusLog.reportPreamp(s.state.preamp)
	}
}

func (s *civControlStruct) setPwr(percent int) error {
	v := uint16(0x0255 * (float64(percent) / 100))
	return s.st.send([]byte{254, 254, civAddress, 224, 0x14, 0x0a, byte(v >> 8), byte(v & 0xff), 253})
}

func (s *civControlStruct) incPwr() error {
	if s.state.pwrPercent < 100 {
		return s.setPwr(s.state.pwrPercent + 1)
	}
	return nil
}

func (s *civControlStruct) decPwr() error {
	if s.state.pwrPercent > 0 {
		return s.setPwr(s.state.pwrPercent - 1)
	}
	return nil
}

func (s *civControlStruct) getDigit(v uint, n int) byte {
	f := float64(v)
	for n > 0 {
		f /= 10
		n--
	}
	return byte(uint(f) % 10)
}

func (s *civControlStruct) incFreq() error {
	return s.setFreq(s.state.freq + s.state.ts)
}

func (s *civControlStruct) decFreq() error {
	return s.setFreq(s.state.freq - s.state.ts)
}

func (s *civControlStruct) setFreq(f uint) error {
	var b [5]byte
	v0 := s.getDigit(f, 9)
	v1 := s.getDigit(f, 8)
	b[4] = v0<<4 | v1
	v0 = s.getDigit(f, 7)
	v1 = s.getDigit(f, 6)
	b[3] = v0<<4 | v1
	v0 = s.getDigit(f, 5)
	v1 = s.getDigit(f, 4)
	b[2] = v0<<4 | v1
	v0 = s.getDigit(f, 3)
	v1 = s.getDigit(f, 2)
	b[1] = v0<<4 | v1
	v0 = s.getDigit(f, 1)
	v1 = s.getDigit(f, 0)
	b[0] = v0<<4 | v1
	if err := s.st.send([]byte{254, 254, civAddress, 224, 5, b[0], b[1], b[2], b[3], b[4], 253}); err != nil {
		return err
	}
	// The transceiver does not send the new freq automatically.
	return s.getFreq()
}

func (s *civControlStruct) incOperatingMode() error {
	s.state.operatingModeIdx++
	if s.state.operatingModeIdx >= len(civOperatingModes) {
		s.state.operatingModeIdx = 0
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) decOperatingMode() error {
	s.state.operatingModeIdx--
	if s.state.operatingModeIdx < 0 {
		s.state.operatingModeIdx = len(civOperatingModes) - 1
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) incFilter() error {
	s.state.filterIdx++
	if s.state.filterIdx >= len(civFilters) {
		s.state.filterIdx = 0
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) decFilter() error {
	s.state.filterIdx--
	if s.state.filterIdx < 0 {
		s.state.filterIdx = len(civFilters) - 1
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) setOperatingModeAndFilter(modeCode, filterCode byte) error {
	if err := s.st.send([]byte{254, 254, civAddress, 224, 0x06, modeCode, filterCode, 253}); err != nil {
		return err
	}
	return s.getMode()
}

func (s *civControlStruct) setPTT(enable bool) error {
	var b byte
	if enable {
		b = 1
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1c, 0, b, 253})
}

func (s *civControlStruct) toggleTune() error {
	if s.state.ptt {
		return nil
	}

	var b byte
	if !s.state.tune {
		b = 2
	} else {
		b = 1
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1c, 1, b, 253})
}

func (s *civControlStruct) toggleDataMode() error {
	var b byte
	var f byte
	if !s.state.dataMode {
		b = 1
		f = 1
	} else {
		b = 0
		f = 0
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1a, 0x06, b, f, 253})
}

func (s *civControlStruct) incBand() error {
	s.state.bandChanging = true
	i := s.state.bandIdx + 1
	if i >= len(civBands) {
		i = 0
	}
	f := civBands[i].freq
	if f == 0 {
		f = (civBands[i].freqFrom + civBands[i].freqTo) / 2
	}
	return s.setFreq(f)
}

func (s *civControlStruct) decBand() error {
	s.state.bandChanging = true
	i := s.state.bandIdx - 1
	if i < 0 {
		i = len(civBands) - 1
	}
	f := civBands[i].freq
	if f == 0 {
		f = civBands[i].freqFrom
	}
	return s.setFreq(f)
}

func (s *civControlStruct) togglePreamp() error {
	b := byte(s.state.preamp + 1)
	if b > 2 {
		b = 0
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x16, 0x02, b, 253})
}

func (s *civControlStruct) incTS() error {
	var b byte
	if s.state.tsValue == 13 {
		b = 0
	} else {
		b = s.state.tsValue + 1
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x10, b, 253})
}

func (s *civControlStruct) decTS() error {
	var b byte
	if s.state.tsValue == 0 {
		b = 13
	} else {
		b = s.state.tsValue - 1
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x10, b, 253})
}

func (s *civControlStruct) getFreq() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 3, 253})
}

func (s *civControlStruct) getMode() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 4, 253})
}

func (s *civControlStruct) getDataMode() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1a, 0x06, 253})
}

func (s *civControlStruct) getTransmitStatus() error {
	if err := s.st.send([]byte{254, 254, civAddress, 224, 0x1c, 0, 253}); err != nil {
		return err
	}
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1c, 1, 253})
}

func (s *civControlStruct) getVd() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 0x15, 0x15, 253})
}

func (s *civControlStruct) getS() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 0x15, 0x02, 253})
}

func (s *civControlStruct) getOVF() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 0x1a, 0x09, 253})
}

func (s *civControlStruct) getTS() error {
	return s.st.send([]byte{254, 254, civAddress, 224, 0x10, 253})
}

func (s *civControlStruct) loop() {
	for {
		select {
		case <-s.deinitNeeded:
			s.deinitFinished <- true
			return
		case <-time.After(sReadInterval):
			_ = s.getS()
			_ = s.getOVF()
		case <-s.resetSReadTimer:
		}
	}
}

func (s *civControlStruct) init(st *serialStream) error {
	s.st = st

	if err := s.getFreq(); err != nil {
		return err
	}
	if err := s.getMode(); err != nil {
		return err
	}
	if err := s.getDataMode(); err != nil {
		return err
	}
	// Querying power.
	if err := s.st.send([]byte{254, 254, civAddress, 224, 0x14, 0x0a, 253}); err != nil {
		return err
	}
	if err := s.getTransmitStatus(); err != nil {
		return err
	}
	// Querying preamp.
	if err := s.st.send([]byte{254, 254, civAddress, 224, 0x16, 0x02, 253}); err != nil {
		return err
	}
	if err := s.getVd(); err != nil {
		return err
	}
	if err := s.getS(); err != nil {
		return err
	}
	if err := s.getOVF(); err != nil {
		return err
	}
	if err := s.getTS(); err != nil {
		return err
	}

	s.deinitNeeded = make(chan bool)
	s.deinitFinished = make(chan bool)
	s.resetSReadTimer = make(chan bool)
	go s.loop()
	return nil
}

func (s *civControlStruct) deinit() {
	if s.deinitNeeded != nil {
		s.deinitNeeded <- true
		<-s.deinitFinished
	}
	s.deinitNeeded = nil
}
