package token

import (
	"fmt"

	"github.com/gogo/protobuf/proto"
)

func (tm *TokenMapping) Clone() (*TokenMapping, error) {
	if tm == nil {
		return nil, fmt.Errorf("nil token mapping")
	}

	marshaledTokenMapping, err := proto.Marshal(tm)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token mapping: %v", err)
	}

	var clonedTokenMapping TokenMapping
	err = proto.Unmarshal(marshaledTokenMapping, &clonedTokenMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal token mapping: %v", err)
	}

	return &clonedTokenMapping, nil
}
