package stickers

import (
	"encoding/json"
	"errors"

	"github.com/status-im/status-go/multiaccounts/settings"
	"github.com/status-im/status-go/services/wallet/bigint"
)

func (api *API) AddPending(chainID uint64, packID *bigint.BigInt) error {
	pendingPacks, err := api.pendingStickerPacks()
	if err != nil {
		return err
	}

	if _, exists := pendingPacks[uint(packID.Uint64())]; exists {
		return errors.New("sticker pack is already pending")
	}

	stickerType, err := api.contractMaker.NewStickerType(chainID)
	if err != nil {
		return err
	}

	stickerPack, err := api.fetchPackData(stickerType, packID.Int, false)
	if err != nil {
		return err
	}

	pendingPacks[uint(packID.Uint64())] = *stickerPack

	return api.accountsDB.SaveSettingField(settings.StickersPacksPending, pendingPacks)
}

func (api *API) pendingStickerPacks() (StickerPackCollection, error) {
	stickerPacks := make(StickerPackCollection)

	pendingStickersJSON, err := api.accountsDB.GetPendingStickerPacks()
	if err != nil {
		return nil, err
	}

	if pendingStickersJSON == nil {
		return stickerPacks, nil
	}

	err = json.Unmarshal(*pendingStickersJSON, &stickerPacks)
	if err != nil {
		return nil, err
	}

	return stickerPacks, nil
}

func (api *API) Pending() (StickerPackCollection, error) {
	stickerPacks, err := api.pendingStickerPacks()
	if err != nil {
		return nil, err
	}

	for packID, stickerPack := range stickerPacks {
		stickerPack.Status = statusPending

		stickerPack.Preview, err = decodeStringHash(stickerPack.Preview)
		if err != nil {
			return nil, err
		}

		stickerPack.Thumbnail, err = decodeStringHash(stickerPack.Thumbnail)
		if err != nil {
			return nil, err
		}

		for i, sticker := range stickerPack.Stickers {
			sticker.URL, err = decodeStringHash(sticker.Hash)
			if err != nil {
				return nil, err
			}
			stickerPack.Stickers[i] = sticker
		}

		stickerPacks[packID] = stickerPack
	}

	return stickerPacks, nil
}

func (api *API) RemovePending(packID *bigint.BigInt) error {
	pendingPacks, err := api.pendingStickerPacks()
	if err != nil {
		return err
	}

	if _, exists := pendingPacks[uint(packID.Uint64())]; !exists {
		return nil
	}

	delete(pendingPacks, uint(packID.Uint64()))

	return api.accountsDB.SaveSettingField(settings.StickersPacksPending, pendingPacks)
}
