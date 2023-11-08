package main

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	mapset "github.com/deckarep/golang-set/v2"

	mxevent "maunium.net/go/mautrix/event"
	mxid "maunium.net/go/mautrix/id"
)

func canonizeTime(strTime string) string {
	strTime = strings.ReplaceAll(strTime, ".", ":")
	if !strings.Contains(strTime, ":") {
		strTime += ":00"
	}
	return strTime
}

func convertTimezone(strTime string, sourceTz, targetTz *time.Location, targetId string) string {
	now := time.Now()
	nowYear, nowMonth, nowDay := now.Year(), int(now.Month()), now.Day()

	parsedTime, err := time.ParseInLocation("15:04", strTime, sourceTz)
	if err != nil {
		panic(err.Error())
	}

	parsedTime = parsedTime.AddDate(nowYear, nowMonth-1, nowDay-1)

	convertedTime := parsedTime.In(targetTz)
	result := fmt.Sprintf("%s %s", convertedTime.Format("15:04"), targetId)

	if convertedTime.Day() > parsedTime.Day() {
		result += " (next day)"
	} else if convertedTime.Day() < parsedTime.Day() {
		result += " (prev day)"
	}

	return result
}

func (session *Session) respondToPing(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	if message == "!buttler ping" {
		session.RespondMessage(logger, roomId, NewBasicTextMessage("Pong!"))
	}
}

func (session *Session) respondToPraise(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	message = strings.ToLower(message)
	if strings.HasPrefix(message, "good bot") {
		session.RespondMessage(logger, roomId, NewBasicTextMessage(":)"))
	}
	if strings.HasPrefix(message, "bad bot") {
		session.RespondMessage(logger, roomId, NewBasicTextMessage(":("))
	}
}

func (session *Session) isTimezoneHintOnCooldown(roomId mxid.RoomID, time string, tzid string) bool {
	key := RequestedTimezoneHint{RoomId: roomId, Time: time, TzId: tzid}
	lastMsgNo, ok := session.LastTzRequests[key]
	if !ok {
		return false
	}

	return session.MessageCounter-lastMsgNo <= session.TimezoneHintCooldown
}

func (session *Session) updateTimezoneHintCooldown(roomId mxid.RoomID, time string, tzid string) {
	key := RequestedTimezoneHint{RoomId: roomId, Time: time, TzId: tzid}
	session.LastTzRequests[key] = session.MessageCounter
}

func (session *Session) respondToTimezoneHints(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	type hint struct {
		time  string
		tzid  string
		tzone *time.Location
	}
	var requiredHints []hint

	for _, tzinfo := range session.Timezones {
		matches := tzinfo.Regex.FindAllStringSubmatch(message, -1)
		if matches == nil {
			continue
		}

		for _, match := range matches {
			time := canonizeTime(match[1])
			if !session.isTimezoneHintOnCooldown(roomId, time, tzinfo.Id) {
				requiredHints = append(requiredHints, hint{time, tzinfo.Id, tzinfo.Timezone})
			} else {
				logger.Debugf("Hint for %s %s still on cooldown", time, tzinfo.Id)
			}
		}
	}

	if requiredHints == nil {
		return
	}

	logger.Debugf("Required timezone hints: %v", requiredHints)

	var responsePlain, responseHtml string

	for i, curHint := range requiredHints {
		if i != 0 {
			responsePlain += "\n"
			responseHtml += "<br>"
		}

		for j, tzinfo := range session.Timezones {
			if j != 0 {
				responsePlain += " = "
				responseHtml += " = "
			}

			var tztext string
			if tzinfo.Id == curHint.tzid {
				tztext = fmt.Sprintf("%s %s", curHint.time, curHint.tzid)
			} else {
				tztext = convertTimezone(curHint.time, curHint.tzone, tzinfo.Timezone, tzinfo.Id)
			}

			tztext = fmt.Sprintf("[%s]", tztext)
			responsePlain += tztext

			tzHtml := "<code>" + template.HTMLEscapeString(tztext) + "</code>"
			if tzinfo.Color != "" {
				tzHtml = fmt.Sprintf("<font color=\"%s\">%s</font>", tzinfo.Color, tzHtml)
			}
			responseHtml += tzHtml
		}
	}

	if session.RespondMessage(logger, roomId, NewHtmlTextMessage(responseHtml, responsePlain)) {
		for _, curHint := range requiredHints {
			session.updateTimezoneHintCooldown(roomId, curHint.time, curHint.tzid)
		}
	}
}

func (session *Session) mentionsToForward(roomId mxid.RoomID, message *mxevent.MessageEventContent) (result []mxid.UserID) {
	resultSet := mapset.NewThreadUnsafeSet[mxid.UserID]()
	for _, userId := range message.Mentions.UserIDs {
		_, ok := session.MentionForwards[userId]
		if ok {
			resultSet.Add(userId)
		}
	}

	for userId, mf := range session.MentionForwards {
		if mf.Regex.MatchString(message.Body) {
			resultSet.Add(userId)
		}
	}

	resultSet.Each(func(userId mxid.UserID) bool {

		state := session.GetMentionForwarderState(roomId, userId)
		if time.Since(state.LastMentionTime) > session.MentionForwardCooldown {
			result = append(result, userId)
		}

		return false
	})

	return
}

func (session *Session) forwardMention(logger logrus.FieldLogger, roomId mxid.RoomID, userId mxid.UserID, messageBody string) error {
	logger.Debugf("Forwarding message %v from room %s to user %s...", messageBody, roomId, userId)

	//TODO

	return nil
}

func (session *Session) respondToMentions(logger logrus.FieldLogger, roomId mxid.RoomID, message *mxevent.MessageEventContent) {
	reports := []string{}
	for _, userId := range session.mentionsToForward(roomId, message) {
		err := session.forwardMention(logger, roomId, userId, message.Body)
		if err != nil {
			logger.Warningf("Failed to forward mention for user %s from room %s: %v", userId, roomId, err)
			reports = append(reports, fmt.Sprintf("Failed to forward mention for user %s: %v", userId, err))
		} else {
			state := session.GetMentionForwarderState(roomId, userId)
			state.LastMentionTime = time.Now()
		}
	}

	if len(reports) > 0 {
		reportString := strings.Join(reports, "\n")
		session.RespondMessage(logger, roomId, NewBasicTextMessage(reportString))
	}
}

func (session *Session) Respond(logger logrus.FieldLogger, roomId mxid.RoomID, message *mxevent.MessageEventContent) {
	logger.Debugf("Message: %v", message.Body)

	defer func() {
		if r := recover(); r != nil {
			logger.Warningf("Error while responding: %v", r)
		}
	}()
	session.respondToPing(logger, roomId, message.Body)
	session.respondToPraise(logger, roomId, message.Body)
	session.respondToTimezoneHints(logger, roomId, message.Body)
	session.respondToMentions(logger, roomId, message)
}
