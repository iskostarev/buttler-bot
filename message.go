package main

import (
	mxevent "maunium.net/go/mautrix/event"
)

type Message interface {
	AsEvent() any
}

type basicTextMessage struct {
	text string
}

type htmlTextMessage struct {
	html, plain string
}

func NewBasicTextMessage(text string) Message {
	return &basicTextMessage{text}
}

func NewHtmlTextMessage(html, plain string) Message {
	return &htmlTextMessage{html, plain}
}

func (msg *basicTextMessage) AsEvent() any {
	return &mxevent.MessageEventContent{
		MsgType: mxevent.MsgText,
		Body:    msg.text,
	}
}

func (msg *htmlTextMessage) AsEvent() any {
	return &mxevent.MessageEventContent{
		MsgType:       mxevent.MsgText,
		Body:          msg.plain,
		Format:        mxevent.FormatHTML,
		FormattedBody: msg.html,
	}
}
