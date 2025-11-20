package main

import (
	"context"

	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type VerificationCallbacks struct {
	VerificationHelper *verificationhelper.VerificationHelper
}

func (vc *VerificationCallbacks) VerificationRequested(ctx context.Context, txnID id.VerificationTransactionID, from id.UserID, fromDevice id.DeviceID) {
	logger := logrus.WithFields(logrus.Fields{
		"txnID":      txnID,
		"from":       from,
		"fromDevice": fromDevice,
	})

	logger.Infof("VerificationRequested")
	vc.VerificationHelper.AcceptVerification(ctx, txnID)
}

func (vc *VerificationCallbacks) VerificationReady(ctx context.Context, txnID id.VerificationTransactionID, otherDeviceID id.DeviceID, supportsSAS, supportsScanQRCode bool, qrCode *verificationhelper.QRCode) {
	logger := logrus.WithFields(logrus.Fields{
		"txnID":              txnID,
		"otherDeviceID":      otherDeviceID,
		"supportsSAS":        supportsSAS,
		"supportsScanQRCode": supportsScanQRCode,
	})

	logger.Infof("VerificationReady")

}

func (vc *VerificationCallbacks) VerificationCancelled(ctx context.Context, txnID id.VerificationTransactionID, code event.VerificationCancelCode, reason string) {
	logger := logrus.WithFields(logrus.Fields{
		"txnID":  txnID,
		"code":   code,
		"reason": reason,
	})

	logger.Infof("VerificationCancelled")
}

// VerificationDone is called when the verification is done.
func (vc *VerificationCallbacks) VerificationDone(ctx context.Context, txnID id.VerificationTransactionID, method event.VerificationMethod) {
	logger := logrus.WithFields(logrus.Fields{
		"txnID":  txnID,
		"method": method,
	})

	logger.Infof("VerificationDone")
}

var _ verificationhelper.RequiredCallbacks = (*VerificationCallbacks)(nil)

func (vc *VerificationCallbacks) ShowSAS(ctx context.Context, txnID id.VerificationTransactionID, emojis []rune, emojiDescriptions []string, decimals []int) {
	logger := logrus.WithFields(logrus.Fields{
		"txnID":             txnID,
		"emojis":            emojis,
		"emojiDescriptions": emojiDescriptions,
		"decimals":          decimals,
	})

	logger.Infof("SAS")

	go func() {
		// Calling ConfirmSAS from a different goroutine, otherwise it seems to deadlock.
		err := vc.VerificationHelper.ConfirmSAS(ctx, txnID)
		if err != nil {
			logger.Errorf("Failed to confirm SAS: %s", err.Error())
		}

		logger.Infof("Confirmed SAS")
	}()
}

var _ verificationhelper.ShowSASCallbacks = (*VerificationCallbacks)(nil)
