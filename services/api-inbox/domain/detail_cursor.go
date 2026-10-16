package domain

import (
	"crypto/hmac"
	"encoding/json"
	"time"
)

const detailCursorTTL = 30 * time.Minute

type detailCursor struct {
	Version   int    `json:"v"`
	PhoneHash string `json:"ph"`
	OwnerHash string `json:"oh"`
	BeforeSK  string `json:"before"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func (c *ListCursorCodec) encodeDetail(ownerID, phone, beforeSK string) (string, error) {
	payload, err := json.Marshal(detailCursor{Version: 1, PhoneHash: HashCursorBinding(phone), OwnerHash: HashCursorBinding(ownerID), BeforeSK: beforeSK, IssuedAt: c.now().Unix(), ExpiresAt: c.now().Add(detailCursorTTL).Unix()})
	if err != nil {
		return "", err
	}
	return encodeSignedCursor(payload, c.sign(payload)), nil
}

func (c *ListCursorCodec) decodeDetail(token, ownerID, phone string) (detailCursor, error) {
	payload, signature, err := splitCursor(token)
	if err != nil || (!hmac.Equal(signature, c.signWith(c.secret, payload)) && !c.matchesPrevious(signature, payload)) {
		return detailCursor{}, ErrInvalidCursor
	}
	var cursor detailCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || cursor.BeforeSK == "" || cursor.PhoneHash != HashCursorBinding(phone) || cursor.OwnerHash != HashCursorBinding(ownerID) || cursor.IssuedAt <= 0 || cursor.ExpiresAt <= cursor.IssuedAt {
		return detailCursor{}, ErrInvalidCursor
	}
	if c.now().Unix() >= cursor.ExpiresAt {
		return detailCursor{}, ErrExpiredCursor
	}
	return cursor, nil
}
