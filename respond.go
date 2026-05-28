package main

import (
	"context"
	"fmt"
	"html/template"
	"runtime/debug"
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

func (session *Session) respondToPing(ctx context.Context, logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	if message == "!buttler ping" {
		session.SendMessage(ctx, logger, roomId, NewBasicTextMessage("Pong!"))
	}
}

func (session *Session) respondToPraise(ctx context.Context, logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	message = strings.ToLower(message)
	if strings.HasPrefix(message, "good bot") {
		session.SendMessage(ctx, logger, roomId, NewBasicTextMessage(":)"))
	}
	if strings.HasPrefix(message, "bad bot") {
		session.SendMessage(ctx, logger, roomId, NewBasicTextMessage(":("))
	}
}

func (session *Session) isTimezoneHintOnCooldown(roomId mxid.RoomID, hintTime string, tzid string) bool {
	key := RequestedTimezoneHint{RoomId: roomId, Time: hintTime, TzId: tzid}
	lastTime, ok := session.LastTzRequests[key]
	if !ok {
		return false
	}

	if session.MessageCounters[roomId]-lastTime.MsgNo > session.TimezoneHintCooldownMsgs {
		return false
	}

	if time.Now().After(lastTime.Timestamp.Add(session.TimezoneHintCooldownDuration)) {
		return false
	}

	return true
}

func (session *Session) updateTimezoneHintCooldown(roomId mxid.RoomID, hintTime string, tzid string) {
	key := RequestedTimezoneHint{RoomId: roomId, Time: hintTime, TzId: tzid}
	session.LastTzRequests[key] = TimezoneRequestTime{
		MsgNo:     session.MessageCounters[roomId],
		Timestamp: time.Now(),
	}
}

func (session *Session) respondToTimezoneHints(ctx context.Context, logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
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

	if session.SendMessage(ctx, logger, roomId, NewHtmlTextMessage(responseHtml, responsePlain)) == nil {
		for _, curHint := range requiredHints {
			session.updateTimezoneHintCooldown(roomId, curHint.time, curHint.tzid)
		}
	}
}

func (session *Session) mentionsToForward(roomId mxid.RoomID, members []mxid.UserID, sender mxid.UserID, message *mxevent.MessageEventContent) (result []mxid.UserID) {
	memberSet := mapset.NewThreadUnsafeSet[mxid.UserID](members...)
	resultSet := mapset.NewThreadUnsafeSet[mxid.UserID]()

	if message.Mentions != nil && message.Mentions.UserIDs != nil {
		for _, userId := range message.Mentions.UserIDs {
			_, ok := session.MentionForwards[userId]
			if ok {
				resultSet.Add(userId)
			}
		}
	}

	if message.Mentions != nil && message.Mentions.Room {
		for userId, _ := range session.MentionForwards {
			if memberSet.Contains(userId) {
				resultSet.Add(userId)
			}
		}
	}

	for userId, mf := range session.MentionForwards {
		if mf.Regex != nil && mf.Regex.MatchString(message.Body) && memberSet.Contains(userId) {
			resultSet.Add(userId)
		}
	}

	resultSet.Each(func(userId mxid.UserID) bool {
		if userId == sender {
			return false
		}

		state := session.GetMentionForwarderState(roomId, userId)
		if time.Since(state.LastMentionTime) > session.MentionForwardCooldown {
			result = append(result, userId)
		}

		return false
	})

	return
}

func (session *Session) forwardMention(ctx context.Context, logger logrus.FieldLogger, msgId mxid.EventID, roomId mxid.RoomID, userId mxid.UserID, message *mxevent.MessageEventContent) error {
	logger.Debugf("Forwarding message %v from room %s to user %s...", message.Body, roomId, userId)

	directRoomId, err := session.FindDirectMessageRoom(ctx, logger, userId)
	if err != nil {
		return err
	}

	if directRoomId == roomId {
		logger.Debugf("Ignoring mention in direct room %s with user %s", roomId, userId)
		return nil
	}

	srcRoomUri := fmt.Sprintf("https://matrix.to/#/%s/%s", roomId, msgId)
	forwardedMessage := NewBasicTextMessage(fmt.Sprintf("%s: %s", srcRoomUri, message.Body))

	return session.SendMessage(ctx, logger, directRoomId, forwardedMessage)
}

func (session *Session) respondToMentions(ctx context.Context, logger logrus.FieldLogger, msgId mxid.EventID, roomId mxid.RoomID, sender mxid.UserID, message *mxevent.MessageEventContent) {
	reports := []string{}
	members, err := session.GetRoomMembers(ctx, roomId)

	if err != nil {
		logger.Errorf("Failed to get member list of room %s: %v", roomId, err)
		reports = append(reports, fmt.Sprintf("Failed to get member list: %v", err))
	}

	logger.Debugf("Member list of room %s: %v", roomId, members)
	for _, userId := range session.mentionsToForward(roomId, members, sender, message) {
		err := session.forwardMention(ctx, logger, msgId, roomId, userId, message)
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
		session.SendMessage(ctx, logger, roomId, NewBasicTextMessage(reportString))
	}
}

func (session *Session) Respond(ctx context.Context, logger logrus.FieldLogger, msgId mxid.EventID, roomId mxid.RoomID, sender mxid.UserID, message *mxevent.MessageEventContent) {
	logger.Debugf("Message: %v", message.Body)

	defer func() {
		if r := recover(); r != nil {
			logger.Warningf("Error while responding: %v\n%s\n", r, string(debug.Stack()))
		}
	}()
	session.respondToPing(ctx, logger, roomId, message.Body)
	session.respondToPraise(ctx, logger, roomId, message.Body)
	session.respondToTimezoneHints(ctx, logger, roomId, message.Body)
	session.respondToMentions(ctx, logger, msgId, roomId, sender, message)
}
