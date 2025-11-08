package main

import "encoding/binary"

// usb_ctrlrequest (Linux / UDC) layout: 8 bytes
type ctrlReq struct {
	bmRequestType uint8
	bRequest      uint8
	wValue        uint16
	wIndex        uint16
	wLength       uint16
}

func parseCtrlReq(b []byte) ctrlReq {
	return ctrlReq{
		bmRequestType: b[0],
		bRequest:      b[1],
		wValue:        binary.LittleEndian.Uint16(b[2:4]),
		wIndex:        binary.LittleEndian.Uint16(b[4:6]),
		wLength:       binary.LittleEndian.Uint16(b[6:8]),
	}
}
