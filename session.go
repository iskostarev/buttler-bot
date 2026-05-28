package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"go.mau.fi/util/dbutil"
	_ "modernc.org/sqlite"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	mxevent "maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	mxid "maunium.net/go/mautrix/id"
)

type RequestedTimezoneHint struct {
	RoomId mxid.RoomID
	Time   string
	TzId   string
}

type TimezoneRequestTime struct {
	MsgNo     int64
	Timestamp time.Time
}

type MentionForward struct {
	Regex *regexp.Regexp
}

type MentionForwarderStateKey struct {
	RoomId mxid.RoomID
	UserId mxid.UserID
}

type MentionForwarderState struct {
	LastMentionTime time.Time
}

type Session struct {
	Client                       *mautrix.Client
	StartTimestamp               int64
	MessageCounters              map[mxid.RoomID]int64
	Timezones                    []TimezoneInfo
	MentionForwards              map[mxid.UserID]MentionForward
	LastTzRequests               map[RequestedTimezoneHint]TimezoneRequestTime
	MentionForwardersState       map[MentionForwarderStateKey]*MentionForwarderState
	TimezoneHintCooldownMsgs     int64
	TimezoneHintCooldownDuration time.Duration
	MentionForwardCooldown       time.Duration
}

type TimezoneInfo struct {
	Id       string
	Color    string
	Timezone *time.Location
	Regex    *regexp.Regexp
}

func buildTimezoneRegexp(innerRegex string) (result *regexp.Regexp, err error) {
	finalRegex := fmt.Sprintf(`(?i)(\d\d?(?:[:.]\d\d)?)\s*(?:%s)`, innerRegex)
	result, err = regexp.Compile(finalRegex)
	return
}

func InitSession(ctx context.Context, config *Config) (session Session, err error) {
	passwordBytes, err := os.ReadFile(config.Matrix.PasswordPath)
	if err != nil {
		return
	}
	password := strings.TrimSpace(string(passwordBytes))

	pickleKeyBytes, err := os.ReadFile(config.Crypto.PickleKeyPath)
	if err != nil {
		return
	}
	pickleKeyBytes = []byte(strings.TrimSpace(string(pickleKeyBytes)))

	session.Client, err = mautrix.NewClient(config.Matrix.HomeserverUrl, "", "")
	if err != nil {
		return
	}
	if session.Client == nil {
		err = errors.New("mautrix.NewClient returned nil")
		return
	}

	reqLogin := mautrix.ReqLogin{
		Type:             mautrix.AuthTypePassword,
		Identifier:       mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: config.Matrix.Username},
		Password:         password,
		StoreCredentials: true,
	}

	if config.Crypto.Enabled {
		logrus.Infof("Initializing CryptoHelper...")
		db, err := dbutil.NewWithDialect(config.Crypto.Database, "sqlite")
		if err != nil {
			return session, err
		}

		cryptoHelper, err := cryptohelper.NewCryptoHelper(session.Client, pickleKeyBytes, db)
		if err != nil {
			return session, err
		}

		cryptoHelper.LoginAs = &reqLogin
		err = cryptoHelper.Init(ctx)
		if err != nil {
			return session, err
		}

		session.Client.Crypto = cryptoHelper

		logrus.Infof("CryptoHelper initialized, logged in")

		verificationCallbacks := &VerificationCallbacks{}
		verificationHelper := verificationhelper.NewVerificationHelper(
			session.Client,
			cryptoHelper.Machine(),
			verificationhelper.NewInMemoryVerificationStore(),
			verificationCallbacks,
			false,
			false,
			true,
		)
		verificationCallbacks.VerificationHelper = verificationHelper

		err = verificationHelper.Init(ctx)
		if err != nil {
			return session, err
		}

		logrus.Infof("VerificationHelper initialized")
	} else {
		_, err = session.Client.Login(ctx, &reqLogin)
		if err != nil {
			return session, err
		}

		logrus.Infof("Logged in")
	}

	session.StartTimestamp = time.Now().UnixMilli()
	session.MessageCounters = make(map[mxid.RoomID]int64)

	for _, tz := range config.Timezones {
		regex, err := buildTimezoneRegexp(tz.Regex)
		if err != nil {
			return session, err
		}

		timezone, err := time.LoadLocation(tz.Timezone)
		if err != nil {
			return session, nil
		}

		tzinfo := TimezoneInfo{Id: tz.Id, Color: tz.Color, Regex: regex, Timezone: timezone}
		session.Timezones = append(session.Timezones, tzinfo)
	}

	session.MentionForwards = make(map[mxid.UserID]MentionForward)
	for _, mfEntry := range config.MentionForwards {
		userId := mxid.UserID(mfEntry.UserId)

		_, repeat := session.MentionForwards[userId]
		if repeat {
			return session, fmt.Errorf("repeating UserId %s in MentionForwards", userId)
		}

		var regex *regexp.Regexp
		if mfEntry.Regex != "" {
			regex, err = regexp.Compile(mfEntry.Regex)
			if err != nil {
				return session, err
			}
		}

		session.MentionForwards[userId] = MentionForward{Regex: regex}
	}

	session.LastTzRequests = make(map[RequestedTimezoneHint]TimezoneRequestTime)
	session.TimezoneHintCooldownMsgs = config.TimezoneHintCooldown.MsgCount
	session.TimezoneHintCooldownDuration, err = time.ParseDuration(config.TimezoneHintCooldown.Duration)
	if err != nil {
		return
	}

	session.MentionForwardCooldown, err = time.ParseDuration(config.MentionForwardCooldown)
	if err != nil {
		return
	}

	session.MentionForwardersState = make(map[MentionForwarderStateKey]*MentionForwarderState)

	return
}

func (session *Session) GetMentionForwarderState(roomId mxid.RoomID, userId mxid.UserID) (result *MentionForwarderState) {
	key := MentionForwarderStateKey{RoomId: roomId, UserId: userId}
	result, ok := session.MentionForwardersState[key]
	if ok {
		return
	}

	result = &MentionForwarderState{}
	session.MentionForwardersState[key] = result

	return
}

func (session *Session) SendMessage(ctx context.Context, logger logrus.FieldLogger, roomId mxid.RoomID, message Message) error {
	if _, err := session.Client.SendMessageEvent(ctx, roomId, mxevent.EventMessage, message.AsEvent()); err != nil {
		logger.Errorf("Failed to respond: %s", err.Error())
		return err
	}
	return nil
}

func (session *Session) FindDirectMessageRoom(ctx context.Context, logger logrus.FieldLogger, userId mxid.UserID) (roomId mxid.RoomID, err error) {
	direct := mxevent.DirectChatsEventContent{}
	if err = session.Client.GetAccountData(ctx, "m.direct", &direct); err != nil {
		return
	}

	directRooms := direct[userId]
	if len(directRooms) == 0 {
		err = errors.New("no direct message room is open")
		return
	}

	roomId = directRooms[0]
	return
}

func (session *Session) GetRoomMembers(ctx context.Context, roomId id.RoomID) ([]mxid.UserID, error) {
	var result []mxid.UserID

	fetched, err := session.Client.StateStore.HasFetchedMembers(ctx, roomId)
	if err != nil {
		return nil, err
	}

	if fetched {
		members, err := session.Client.StateStore.GetAllMembers(ctx, roomId)
		if err != nil {
			return nil, err
		}
		for member, membEvent := range members {
			if membEvent.Membership == mxevent.MembershipJoin {
				result = append(result, member)
			}
		}
	} else {
		members, err := session.Client.Members(ctx, roomId)
		if err != nil {
			return nil, err
		}

		for _, evt := range members.Chunk {
			if evt.Content.AsMember().Membership == mxevent.MembershipJoin {
				result = append(result, mxid.UserID(evt.GetStateKey()))
			}
		}
	}

	return result, nil
}

func (session *Session) handleMessage(ctx context.Context, evt *mxevent.Event) {
	session.MessageCounters[evt.RoomID]++

	if evt.Sender == session.Client.UserID {
		return
	}

	if evt.Timestamp < session.StartTimestamp {
		return
	}

	logger := logrus.WithFields(logrus.Fields{
		"event_id": evt.ID,
		"room_id":  evt.RoomID,
		"sender":   evt.Sender,
		"msgno":    session.MessageCounters[evt.RoomID],
	})

	session.Respond(ctx, logger, evt.ID, evt.RoomID, evt.Sender, evt.Content.AsMessage())

	if err := session.Client.MarkRead(ctx, evt.RoomID, evt.ID); err != nil {
		logger.Errorf("Failed to mark as read: %s", err.Error())
	}
}

func (session *Session) handleMembership(ctx context.Context, evt *mxevent.Event) {
	emem := evt.Content.AsMember()
	if emem.Membership != mxevent.MembershipInvite {
		return
	}

	_, err := session.Client.JoinRoomByID(context.TODO(), evt.RoomID)
	if err != nil {
		logrus.Errorf("Failed to join room %s: %s", evt.RoomID, err.Error())
		return
	}

	logrus.Infof("Joined room %s", evt.RoomID)

	if emem.IsDirect {
		content := &mxevent.DirectChatsEventContent{evt.Sender: []mxid.RoomID{evt.RoomID}}
		err := session.Client.SetAccountData(context.TODO(), "m.direct", content)
		if err != nil {
			logrus.Errorf("Failed to mark room %s (with user %s) as direct", evt.RoomID, evt.Sender)
		} else {
			logrus.Infof("Marked room %s (with user %s) as direct", evt.RoomID, evt.Sender)
		}
	}
}

func (session *Session) RunSyncLoop() {
	syncer := session.Client.Syncer.(*mautrix.DefaultSyncer)

	syncer.OnEventType(mxevent.EventMessage, session.handleMessage)
	syncer.OnEventType(mxevent.StateMember, session.handleMembership)

	if err := session.Client.Sync(); err != nil {
		panic(errors.Wrap(err, "Sync failed"))
	}
}
