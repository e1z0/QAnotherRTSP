package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	supersyncsdk "github.com/e1z0/qanotherrtsp/supersync/sdk/go"
	"github.com/mappu/miqt/qt"
)

var superSync = newSuperSyncManager()

const superSyncPollInterval = 15 * time.Second

type superSyncManager struct {
	mu sync.Mutex
}

type superSyncMetadata struct {
	BucketID      string                          `json:"bucket_id,omitempty"`
	UserID        string                          `json:"user_id,omitempty"`
	DeviceID      string                          `json:"device_id,omitempty"`
	AccessToken   string                          `json:"access_token,omitempty"`
	RefreshToken  string                          `json:"refresh_token,omitempty"`
	AccessExpiry  time.Time                       `json:"access_expiry,omitempty"`
	Profiles      map[string]superSyncProfileMeta `json:"profiles,omitempty"`
	LastError     string                          `json:"last_error,omitempty"`
	LastSyncAt    time.Time                       `json:"last_sync_at,omitempty"`
	LastSyncState string                          `json:"last_sync_state,omitempty"`
}

type superSyncProfileMeta struct {
	ItemID            string    `json:"item_id,omitempty"`
	LastSyncedVersion int       `json:"last_synced_version,omitempty"`
	LastSyncedSHA256  string    `json:"last_synced_sha256,omitempty"`
	LastSyncAt        time.Time `json:"last_sync_at,omitempty"`
	Dirty             bool      `json:"dirty,omitempty"`
	Conflicted        bool      `json:"conflicted,omitempty"`
}

type superSyncEnvelope struct {
	Encrypted   bool   `json:"encrypted"`
	CipherSuite string `json:"cipher_suite,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	WrappedKey  string `json:"wrapped_key,omitempty"`
	Payload     string `json:"payload"`
}

func newSuperSyncManager() *superSyncManager {
	return &superSyncManager{}
}

func (m *superSyncManager) SyncCurrentConfig(reason string) error {
	configMu.Lock()
	cfg := cloneAppConfig(globalConfig)
	configMu.Unlock()
	return m.SyncConfig(cfg, reason)
}

func (m *superSyncManager) SyncCurrentConfigAsync(reason string) {
	go func() {
		if err := m.SyncCurrentConfig(reason); err != nil {
			log.Printf("supersync async sync failed: %v", err)
		}
	}()
}

func (m *superSyncManager) StartPolling() {
	go func() {
		ticker := time.NewTicker(superSyncPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			configMu.Lock()
			enabled := globalConfig.SuperSync.Enabled
			configMu.Unlock()
			if !enabled || appQuitting.Load() {
				continue
			}
			if err := m.SyncCurrentConfig("poll"); err != nil {
				log.Printf("supersync poll failed: %v", err)
			}
		}
	}()
}

func (m *superSyncManager) SyncConfig(cfg AppConfig, reason string) error {
	ensureSuperSyncDefaults(&cfg)
	if !cfg.SuperSync.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.SuperSync.BaseURL) == "" {
		return errors.New("supersync base URL is required")
	}
	if strings.TrimSpace(cfg.SuperSync.BucketID) == "" {
		return errors.New("supersync bucket ID is required")
	}
	if strings.TrimSpace(cfg.SuperSync.UserID) == "" {
		return errors.New("supersync user ID is required")
	}
	if strings.TrimSpace(cfg.SuperSync.SigningKeyPath) == "" {
		return errors.New("supersync signing key path is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := loadSuperSyncMetadata()
	if err != nil {
		return err
	}
	meta.BucketID = cfg.SuperSync.BucketID
	meta.UserID = cfg.SuperSync.UserID
	meta.DeviceID = cfg.SuperSync.DeviceID

	profileID := cfg.SuperSync.ProfileID
	if profileID == "" {
		profileID = "default"
	}
	pmeta := meta.profile(profileID)
	firstSync := pmeta.LastSyncedVersion == 0 && pmeta.LastSyncedSHA256 == "" && pmeta.ItemID == ""

	payload, digest, err := marshalCanonicalSyncPayload(cfg)
	if err != nil {
		return err
	}
	localDirty := pmeta.LastSyncedSHA256 != digest
	pmeta.Dirty = localDirty
	meta.Profiles[profileID] = pmeta
	if err := saveSuperSyncMetadata(meta); err != nil {
		return err
	}

	m.updateStatus("sync_start", time.Time{}, "")
	log.Printf("supersync event=sync_start reason=%s profile=%s", reason, profileID)

	signingKey, err := loadSuperSyncSigningKey(cfg.SuperSync.SigningKeyPath)
	if err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}

	client, err := m.authenticatedClient(cfg, meta, signingKey)
	if err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}

	itemKey := superSyncItemKey(profileID)
	remoteItem, err := findRemoteSettingsItem(client, cfg.SuperSync.BucketID, itemKey, cfg.SuperSync.SharedProfile)
	if err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}
	if err := refreshLiveSyncInputs(reason, &cfg, &payload, &digest, &pmeta); err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}
	localDirty = pmeta.LastSyncedSHA256 != digest
	pmeta.Dirty = localDirty
	meta.Profiles[profileID] = pmeta
	if err := saveSuperSyncMetadata(meta); err != nil {
		return err
	}

	if remoteItem == nil {
		upload, err := pushSettingsPayload(client, cfg, payload, digest, "", 0)
		if err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		pmeta.ItemID = upload.Item.ID
		pmeta.LastSyncedVersion = upload.Version.Version
		pmeta.LastSyncedSHA256 = digest
		pmeta.LastSyncAt = time.Now().UTC()
		pmeta.Dirty = false
		pmeta.Conflicted = false
		meta.Profiles[profileID] = pmeta
		if err := saveSuperSyncMetadata(meta); err != nil {
			return err
		}
		if err := saveSuperSyncSnapshot(profileID, payload); err != nil {
			log.Printf("supersync snapshot save failed: %v", err)
		}
		if err := reconcileShares(client, cfg, upload.Item.ID); err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		m.updateStatus("push_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=push_applied profile=%s size=%d", profileID, len(payload))
		return nil
	}

	remotePayload, remoteDigest, remoteVersion, err := downloadSettingsPayload(client, cfg, *remoteItem)
	if err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}
	if err := refreshLiveSyncInputs(reason, &cfg, &payload, &digest, &pmeta); err != nil {
		m.updateStatus("sync_failure", time.Time{}, err.Error())
		return err
	}
	localDirty = pmeta.LastSyncedSHA256 != digest

	remoteChanged := remoteVersion != 0 && (remoteVersion != pmeta.LastSyncedVersion || remoteDigest != pmeta.LastSyncedSHA256)
	if firstSync && remoteChanged {
		if !cfg.SuperSync.AllowPullApply {
			err := errors.New("remote settings exist but pull apply is disabled")
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		if err := applyDownloadedConfig(remotePayload, cfg); err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		pmeta.ItemID = remoteItem.ID
		pmeta.LastSyncedVersion = remoteVersion
		pmeta.LastSyncedSHA256 = remoteDigest
		pmeta.LastSyncAt = time.Now().UTC()
		pmeta.Dirty = false
		pmeta.Conflicted = false
		meta.Profiles[profileID] = pmeta
		if err := saveSuperSyncMetadata(meta); err != nil {
			return err
		}
		if err := saveSuperSyncSnapshot(profileID, remotePayload); err != nil {
			log.Printf("supersync snapshot save failed: %v", err)
		}
		m.updateStatus("pull_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=bootstrap_pull_applied profile=%s size=%d", profileID, len(remotePayload))
		return nil
	}

	if !localDirty && remoteChanged {
		if !cfg.SuperSync.AllowPullApply {
			err := errors.New("remote settings changed but pull apply is disabled")
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		if err := applyDownloadedConfig(remotePayload, cfg); err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		pmeta.ItemID = remoteItem.ID
		pmeta.LastSyncedVersion = remoteVersion
		pmeta.LastSyncedSHA256 = remoteDigest
		pmeta.LastSyncAt = time.Now().UTC()
		pmeta.Dirty = false
		pmeta.Conflicted = false
		meta.Profiles[profileID] = pmeta
		if err := saveSuperSyncMetadata(meta); err != nil {
			return err
		}
		if err := saveSuperSyncSnapshot(profileID, remotePayload); err != nil {
			log.Printf("supersync snapshot save failed: %v", err)
		}
		m.updateStatus("pull_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=pull_applied profile=%s size=%d", profileID, len(remotePayload))
		return nil
	}
	if !localDirty && !remoteChanged {
		now := time.Now().UTC()
		pmeta.ItemID = remoteItem.ID
		pmeta.LastSyncedVersion = remoteVersion
		pmeta.LastSyncedSHA256 = remoteDigest
		pmeta.LastSyncAt = now
		pmeta.Dirty = false
		pmeta.Conflicted = false
		meta.Profiles[profileID] = pmeta
		if err := saveSuperSyncMetadata(meta); err != nil {
			return err
		}
		if err := saveSuperSyncSnapshot(profileID, remotePayload); err != nil {
			log.Printf("supersync snapshot save failed: %v", err)
		}
		m.updateStatus("up_to_date", now, "")
		log.Printf("supersync event=up_to_date profile=%s", profileID)
		return nil
	}

	if localDirty {
		if remoteChanged {
			basePayload, _ := loadSuperSyncSnapshot(profileID)
			preferRemote := reason == "poll" || reason == "startup"
			mergedPayload, resolution, mergeErr := autoResolveSettingsConflict(basePayload, payload, remotePayload, preferRemote)
			if mergeErr == nil {
				mergedDigest := sha256Hex(mergedPayload)
				if bytesEqual(mergedPayload, remotePayload) {
					if err := applyDownloadedConfig(remotePayload, cfg); err != nil {
						m.updateStatus("sync_failure", time.Time{}, err.Error())
						return err
					}
					pmeta.ItemID = remoteItem.ID
					pmeta.LastSyncedVersion = remoteVersion
					pmeta.LastSyncedSHA256 = remoteDigest
					pmeta.LastSyncAt = time.Now().UTC()
					pmeta.Dirty = false
					pmeta.Conflicted = false
					meta.Profiles[profileID] = pmeta
					if err := saveSuperSyncMetadata(meta); err != nil {
						return err
					}
					if err := saveSuperSyncSnapshot(profileID, remotePayload); err != nil {
						log.Printf("supersync snapshot save failed: %v", err)
					}
					m.updateStatus("pull_applied", pmeta.LastSyncAt, "")
					log.Printf("supersync event=conflict_auto_resolved profile=%s resolution=%s action=pull", profileID, resolution)
					return nil
				}

				upload, err := pushSettingsPayload(client, cfg, mergedPayload, mergedDigest, remoteItem.ID, remoteVersion)
				if err == nil {
					if err := applyDownloadedConfig(mergedPayload, cfg); err != nil {
						m.updateStatus("sync_failure", time.Time{}, err.Error())
						return err
					}
					pmeta.ItemID = upload.Item.ID
					pmeta.LastSyncedVersion = upload.Version.Version
					pmeta.LastSyncedSHA256 = mergedDigest
					pmeta.LastSyncAt = time.Now().UTC()
					pmeta.Dirty = false
					pmeta.Conflicted = false
					meta.Profiles[profileID] = pmeta
					if err := saveSuperSyncMetadata(meta); err != nil {
						return err
					}
					if err := saveSuperSyncSnapshot(profileID, mergedPayload); err != nil {
						log.Printf("supersync snapshot save failed: %v", err)
					}
					m.updateStatus("push_applied", pmeta.LastSyncAt, "")
					log.Printf("supersync event=conflict_auto_resolved profile=%s resolution=%s action=push", profileID, resolution)
					return nil
				}
				log.Printf("supersync auto-merge push failed: %v", err)
			} else {
				log.Printf("supersync auto-merge failed: %v", mergeErr)
			}
			if err := persistConflictArtifacts(payload, remotePayload); err != nil {
				m.updateStatus("sync_failure", time.Time{}, err.Error())
				return err
			}
			pmeta.Conflicted = true
			pmeta.Dirty = true
			meta.Profiles[profileID] = pmeta
			if err := saveSuperSyncMetadata(meta); err != nil {
				return err
			}
			err := errors.New("supersync conflict detected; local and remote snapshots were saved to the conflict directory")
			m.updateStatus("conflict_detected", time.Now().UTC(), err.Error())
			log.Printf("supersync event=conflict_detected profile=%s", profileID)
			return err
		}

		upload, err := pushSettingsPayload(client, cfg, payload, digest, remoteItem.ID, pmeta.LastSyncedVersion)
		if err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		pmeta.ItemID = upload.Item.ID
		pmeta.LastSyncedVersion = upload.Version.Version
		pmeta.LastSyncedSHA256 = digest
		pmeta.LastSyncAt = time.Now().UTC()
		pmeta.Dirty = false
		pmeta.Conflicted = false
		meta.Profiles[profileID] = pmeta
		if err := saveSuperSyncMetadata(meta); err != nil {
			return err
		}
		if err := saveSuperSyncSnapshot(profileID, payload); err != nil {
			log.Printf("supersync snapshot save failed: %v", err)
		}
		if err := reconcileShares(client, cfg, upload.Item.ID); err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		m.updateStatus("push_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=push_applied profile=%s size=%d", profileID, len(payload))
	}

	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *superSyncManager) updateStatus(state string, at time.Time, errText string) {
	configMu.Lock()
	globalConfig.SuperSync.LastSyncStatus = state
	if !at.IsZero() {
		globalConfig.SuperSync.LastSyncAt = at
	}
	globalConfig.SuperSync.LastError = errText
	configMu.Unlock()
	if err := SaveConfig(); err != nil {
		log.Printf("supersync status save failed: %v", err)
	}
}

func (m *superSyncManager) ensureDiscovery(cfg AppConfig, _ *superSyncMetadata) error {
	_, err := supersyncsdk.New(cfg.SuperSync.BaseURL).DiscoveryTyped(context.Background())
	return err
}

func (m *superSyncManager) ensureAccessToken(cfg AppConfig, meta *superSyncMetadata) error {
	key, err := loadSuperSyncSigningKey(cfg.SuperSync.SigningKeyPath)
	if err != nil {
		return err
	}
	_, err = m.authenticatedClient(cfg, meta, key)
	return err
}

func (m *superSyncManager) authenticatedClient(cfg AppConfig, meta *superSyncMetadata, signingKey ed25519.PrivateKey) (*supersyncsdk.Client, error) {
	client := supersyncsdk.New(cfg.SuperSync.BaseURL)
	if _, err := client.DiscoveryTyped(context.Background()); err != nil {
		return nil, err
	}
	if meta.AccessToken != "" && time.Until(meta.AccessExpiry) > time.Minute {
		return client.WithToken(meta.AccessToken), nil
	}
	if meta.RefreshToken != "" {
		verify, err := client.RefreshToken(context.Background(), meta.RefreshToken, signingKey)
		if err == nil {
			log.Printf("supersync event=auth_refresh_success")
			updateTokenState(meta, verify)
			if err := saveSuperSyncMetadata(meta); err != nil {
				return nil, err
			}
			return client.WithToken(meta.AccessToken), nil
		}
		log.Printf("supersync event=auth_refresh_failure")
		meta.AccessToken = ""
		meta.RefreshToken = ""
		meta.AccessExpiry = time.Time{}
	}

	challenge, err := client.CreateChallenge(context.Background(), cfg.SuperSync.BucketID, cfg.SuperSync.UserID)
	if err != nil {
		return nil, err
	}
	challengePayload := strings.TrimSpace(challenge.Message)
	if challengePayload == "" {
		challengePayload = strings.TrimSpace(challenge.Nonce)
	}
	if challengePayload == "" {
		return nil, errors.New("supersync challenge response is missing both message and nonce")
	}
	signature := ed25519.Sign(signingKey, []byte(challengePayload))
	verify, err := client.VerifyChallenge(context.Background(), challenge.ChallengeID, signature)
	if err != nil {
		return nil, err
	}
	if verify.BucketID != "" && verify.BucketID != cfg.SuperSync.BucketID {
		return nil, fmt.Errorf("supersync verify returned bucket %q, expected %q", verify.BucketID, cfg.SuperSync.BucketID)
	}
	if verify.UserID != "" && verify.UserID != cfg.SuperSync.UserID {
		return nil, fmt.Errorf("supersync verify returned user %q, expected %q", verify.UserID, cfg.SuperSync.UserID)
	}
	updateTokenState(meta, verify)
	if err := saveSuperSyncMetadata(meta); err != nil {
		return nil, err
	}
	return client.WithToken(meta.AccessToken), nil
}

func updateTokenState(meta *superSyncMetadata, verify supersyncsdk.VerifyResponse) {
	meta.AccessToken = verify.AccessToken
	meta.RefreshToken = verify.RefreshToken
	meta.AccessExpiry = parseTimeLoose(verify.ExpiresAt)
	if meta.AccessExpiry.IsZero() {
		meta.AccessExpiry = time.Now().Add(15 * time.Minute)
	}
}

func pushSettingsPayload(client *supersyncsdk.Client, cfg AppConfig, payload []byte, digest, itemID string, expectedVersion int) (supersyncsdk.UploadItemResponse, error) {
	req, err := buildUploadRequest(cfg, payload, digest, expectedVersion)
	if err != nil {
		return supersyncsdk.UploadItemResponse{}, err
	}
	if itemID == "" {
		req.LogicalKey = superSyncItemKey(cfg.SuperSync.ProfileID)
		return client.UploadJSONWithInputTyped(context.Background(), cfg.SuperSync.BucketID, req)
	}
	return client.UploadItemVersionJSONWithInputTyped(context.Background(), cfg.SuperSync.BucketID, itemID, supersyncsdk.UploadItemVersionJSONRequest{
		ContentType:      req.ContentType,
		CiphertextBase64: req.CiphertextBase64,
		ExpectedVersion:  req.ExpectedVersion,
		SHA256:           req.SHA256,
		CipherSuite:      req.CipherSuite,
		WrappedKey:       req.WrappedKey,
		Nonce:            req.Nonce,
		Tags:             req.Tags,
		Metadata:         req.Metadata,
		TTLSeconds:       req.TTLSeconds,
	})
}

func buildUploadRequest(cfg AppConfig, payload []byte, plaintextDigest string, expectedVersion int) (supersyncsdk.UploadJSONRequest, error) {
	ciphertext, cipherSuite, wrappedKey, nonce, err := encodePayloadForUpload(cfg, payload)
	if err != nil {
		return supersyncsdk.UploadJSONRequest{}, err
	}
	uploadedDigest := sha256Hex(ciphertext)
	return supersyncsdk.UploadJSONRequest{
		ContentType:      "application/json",
		CiphertextBase64: base64.StdEncoding.EncodeToString(ciphertext),
		ExpectedVersion:  expectedVersion,
		SHA256:           uploadedDigest,
		CipherSuite:      cipherSuite,
		WrappedKey:       wrappedKey,
		Nonce:            nonce,
		Metadata: map[string]any{
			"profile_id":         cfg.SuperSync.ProfileID,
			"schema_version":     "1",
			"client_device_id":   cfg.SuperSync.DeviceID,
			"updated_by_user_id": cfg.SuperSync.UserID,
			"updated_at":         time.Now().UTC().Format(time.RFC3339),
			"payload_sha256":     plaintextDigest,
			"upload_sha256":      uploadedDigest,
			"encrypted":          cfg.SuperSync.PrivateProfile,
		},
	}, nil
}

func findRemoteSettingsItem(client *supersyncsdk.Client, bucketID, logicalKey string, includeShared bool) (*supersyncsdk.ItemRecord, error) {
	items, err := client.ListItemsWithFilterOptionsTyped(context.Background(), bucketID, supersyncsdk.ItemListFilter{
		IncludeShared:    includeShared,
		LogicalKeyPrefix: logicalKey,
		Limit:            100,
	})
	if err != nil {
		return nil, err
	}
	var best *supersyncsdk.ItemRecord
	for i := range items.Items {
		item := items.Items[i]
		if item.LogicalKey != logicalKey || item.IsArchived {
			continue
		}
		if best == nil || item.UpdatedAt.After(best.UpdatedAt) {
			copyItem := item
			best = &copyItem
		}
	}
	return best, nil
}

func downloadSettingsPayload(client *supersyncsdk.Client, cfg AppConfig, item supersyncsdk.ItemRecord) ([]byte, string, int, error) {
	versions, err := client.ListVersionsWithPaginationTyped(context.Background(), cfg.SuperSync.BucketID, item.ID, 1, 0)
	if err != nil {
		return nil, "", 0, err
	}
	if len(versions.Versions) == 0 {
		return nil, "", 0, errors.New("remote item has no versions")
	}
	version := versions.Versions[0]
	ciphertext, _, err := client.DownloadVersionWithSHA256(context.Background(), cfg.SuperSync.BucketID, item.ID, version.Version, version.SHA256)
	if err != nil {
		return nil, "", 0, err
	}
	uploadedDigest := sha256Hex(ciphertext)
	if version.SHA256 != "" && uploadedDigest != version.SHA256 {
		return nil, "", 0, fmt.Errorf("uploaded payload checksum mismatch: got %s want %s", uploadedDigest, version.SHA256)
	}
	plaintext, err := decodeDownloadedPayload(cfg, ciphertext, version)
	if err != nil {
		return nil, "", 0, err
	}
	digest := sha256Hex(plaintext)
	return plaintext, digest, version.Version, nil
}

func reconcileShares(client *supersyncsdk.Client, cfg AppConfig, itemID string) error {
	if !cfg.SuperSync.SharedProfile || len(cfg.SuperSync.ShareRecipients) == 0 {
		return nil
	}
	for _, recipient := range cfg.SuperSync.ShareRecipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if _, err := client.ShareItemTyped(context.Background(), cfg.SuperSync.BucketID, itemID, recipient, "read", ""); err != nil {
			apiErr, ok := supersyncsdk.AsAPIError(err)
			if ok && apiErr.StatusCode == 409 {
				continue
			}
			return err
		}
	}
	return nil
}

func loadSuperSyncMetadata() (*superSyncMetadata, error) {
	path := superSyncMetadataPath()
	meta := &superSyncMetadata{Profiles: map[string]superSyncProfileMeta{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return meta, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, meta); err != nil {
		return nil, err
	}
	if meta.Profiles == nil {
		meta.Profiles = map[string]superSyncProfileMeta{}
	}
	return meta, nil
}

func saveSuperSyncMetadata(meta *superSyncMetadata) error {
	if meta == nil {
		return nil
	}
	if meta.Profiles == nil {
		meta.Profiles = map[string]superSyncProfileMeta{}
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := superSyncMetadataPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *superSyncMetadata) profile(profileID string) superSyncProfileMeta {
	if m.Profiles == nil {
		m.Profiles = map[string]superSyncProfileMeta{}
	}
	return m.Profiles[profileID]
}

func superSyncMetadataPath() string {
	return filepath.Join(env.configDir, "supersync-meta.json")
}

func superSyncConflictDir() string {
	return filepath.Join(env.configDir, "supersync-conflicts")
}

func superSyncQuarantineDir() string {
	return filepath.Join(env.configDir, "supersync-quarantine")
}

func superSyncSnapshotDir() string {
	return filepath.Join(env.configDir, "supersync-snapshots")
}

func superSyncSnapshotPath(profileID string) string {
	return filepath.Join(superSyncSnapshotDir(), sanitizeFSComponent(profileID)+".json")
}

func superSyncItemKey(profileID string) string {
	if profileID == "" || profileID == "default" {
		return "settings/default"
	}
	return "settings/profiles/" + sanitizeFSComponent(profileID)
}

func marshalCanonicalSyncPayload(cfg AppConfig) ([]byte, string, error) {
	syncCfg := cloneAppConfig(cfg)
	_ = ensureCameraIDs(syncCfg.Cameras)
	for i := range syncCfg.Cameras {
		syncCfg.Cameras[i].X = 0
		syncCfg.Cameras[i].Y = 0
		syncCfg.Cameras[i].Width = 0
		syncCfg.Cameras[i].Height = 0
	}
	// Screen-specific saved layouts stay local to each device.
	syncCfg.Formations = nil
	syncCfg.LastFormation = ""
	slices.Sort(syncCfg.SuperSync.ShareRecipients)
	syncCfg.SuperSync.DeviceID = ""
	syncCfg.SuperSync.SigningKeyPath = ""
	syncCfg.SuperSync.EncryptionPassphrase = ""
	syncCfg.SuperSync.LastSyncStatus = ""
	syncCfg.SuperSync.LastSyncAt = time.Time{}
	syncCfg.SuperSync.LastError = ""
	b, err := json.Marshal(syncCfg)
	if err != nil {
		return nil, "", err
	}
	return b, sha256Hex(b), nil
}

func applyDownloadedConfig(payload []byte, local AppConfig) error {
	configMu.Lock()
	liveLocal := cloneAppConfig(globalConfig)
	configMu.Unlock()

	var remoteCfg AppConfig
	if err := json.Unmarshal(payload, &remoteCfg); err != nil {
		if err := quarantinePayload(payload); err != nil {
			return err
		}
		return fmt.Errorf("remote settings schema invalid; payload quarantined: %w", err)
	}
	remoteCfg.SuperSync.DeviceID = local.SuperSync.DeviceID
	remoteCfg.SuperSync.SigningKeyPath = local.SuperSync.SigningKeyPath
	remoteCfg.SuperSync.EncryptionPassphrase = local.SuperSync.EncryptionPassphrase
	remoteCfg.SuperSync.LastSyncStatus = liveLocal.SuperSync.LastSyncStatus
	remoteCfg.SuperSync.LastSyncAt = liveLocal.SuperSync.LastSyncAt
	remoteCfg.SuperSync.LastError = liveLocal.SuperSync.LastError
	remoteCfg.Formations = liveLocal.Formations
	remoteCfg.LastFormation = liveLocal.LastFormation
	preserveLocalCameraGeometry(&remoteCfg, liveLocal)
	ensureSuperSyncDefaults(&remoteCfg)
	_ = ensureCameraIDs(remoteCfg.Cameras)

	configMu.Lock()
	globalConfig = remoteCfg
	configMu.Unlock()
	if err := SaveConfig(); err != nil {
		return err
	}
	if tray == nil && len(wins) == 0 {
		return nil
	}
	CallOnQtMain(func() {
		applyRuntimeConfigUpdate(remoteCfg)
	})
	return nil
}

func applyRuntimeConfigUpdate(newCfg AppConfig) {
	currentWins := wins
	existingByID := make(map[string]*CamWindow, len(currentWins))
	used := make(map[*CamWindow]bool, len(currentWins))
	for _, w := range currentWins {
		if w == nil {
			continue
		}
		id := w.cfg.ID
		if id == "" {
			id = cameraRuntimeKey(w.cfg)
		}
		existingByID[id] = w
	}

	newWins := make([]*CamWindow, len(newCfg.Cameras))
	for i := range newCfg.Cameras {
		cam := newCfg.Cameras[i]
		key := cameraRuntimeKey(cam)
		if w, ok := existingByID[key]; ok && w != nil {
			used[w] = true
			w.idx = i
			w.idKey = key
			if cam.Disabled {
				w.SuppressOnClosedOnce()
				w.Close()
				newWins[i] = nil
				continue
			}
			newWins[i] = w
			if !cameraSyncEquivalent(w.cfg, cam) {
				go w.RestartWith(cam, "supersync-apply")
			} else {
				w.cfg = cam
			}
			continue
		}
		if cam.Disabled {
			continue
		}
		w, err := newCamWindow(cam, i)
		if err != nil {
			log.Printf("supersync apply: open cam %q failed: %v", safeCamTitle(cam), err)
			continue
		}
		newWins[i] = w
	}

	for _, w := range currentWins {
		if w == nil || used[w] {
			continue
		}
		w.SuppressOnClosedOnce()
		w.Close()
	}
	wins = newWins

	applyRuntimeWindowSettings()

	if tray != nil {
		tray.cfg = &globalConfig
		tray.rebuild()
		for i, w := range wins {
			if w == nil {
				continue
			}
			tray.AttachWindowHooks(i, w)
		}
	}
}

func applyRuntimeWindowSettings() {
	for i, w := range wins {
		if w == nil || w.win == nil {
			continue
		}
		if i < len(globalConfig.Cameras) {
			w.cfg = globalConfig.Cameras[i]
		}
		atop := globalConfig.AlwaysOnTopAll
		if !atop && i < len(globalConfig.Cameras) {
			atop = globalConfig.Cameras[i].AlwaysOnTop
		}
		w.win.SetWindowFlag2(qt.WindowStaysOnTopHint, atop)
		w.win.SetWindowFlag2(qt.FramelessWindowHint, globalConfig.NoWindowsTitles)
		if w.view != nil {
			w.view.SetOverlayTitle(safeCamTitle(w.cfg), globalConfig.NoWindowsTitles)
		}
		if !globalConfig.NoWindowsTitles {
			w.win.SetWindowTitle("Cam: " + safeCamTitle(w.cfg))
		}
		w.win.Show()
		w.ApplyGuiRefreshSettings()
	}
}

func cameraRuntimeKey(c CameraConfig) string {
	if c.ID != "" {
		return c.ID
	}
	if c.Name != "" {
		return c.Name
	}
	return c.URL
}

func cameraSyncEquivalent(a, b CameraConfig) bool {
	return a.ID == b.ID &&
		a.Name == b.Name &&
		a.Disabled == b.Disabled &&
		a.URL == b.URL &&
		a.RTSPTCP == b.RTSPTCP &&
		a.Caching == b.Caching &&
		a.AlwaysOnTop == b.AlwaysOnTop &&
		a.Mute == b.Mute &&
		a.Stretch == b.Stretch &&
		a.FFmpegParams == b.FFmpegParams &&
		intPtrEqual(a.Volume, b.Volume) &&
		a.Probesize == b.Probesize &&
		a.AnalyzeUS == b.AnalyzeUS &&
		a.Threads == b.Threads &&
		a.HwAccel == b.HwAccel
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func preserveLocalCameraGeometry(dst *AppConfig, local AppConfig) {
	if dst == nil {
		return
	}
	type geom struct {
		x, y int
		w, h int
	}
	byKey := make(map[string]geom, len(local.Cameras))
	for i := range local.Cameras {
		c := local.Cameras[i]
		key := c.ID
		if key == "" {
			if c.Name != "" {
				key = c.Name
			} else {
				key = c.URL
			}
		}
		byKey[key] = geom{x: c.X, y: c.Y, w: c.Width, h: c.Height}
	}
	for i := range dst.Cameras {
		c := &dst.Cameras[i]
		key := c.ID
		if key == "" {
			if c.Name != "" {
				key = c.Name
			} else {
				key = c.URL
			}
		}
		if g, ok := byKey[key]; ok {
			c.X = g.x
			c.Y = g.y
			c.Width = g.w
			c.Height = g.h
		}
	}
}

func refreshLiveSyncInputs(reason string, cfg *AppConfig, payload *[]byte, digest *string, pmeta *superSyncProfileMeta) error {
	if !syncReasonUsesLiveGlobalState(reason) {
		return nil
	}
	liveCfg, livePayload, liveDigest, err := currentLiveSyncSnapshot()
	if err != nil {
		return err
	}
	if superSyncTargetChanged(*cfg, liveCfg) {
		return errors.New("supersync configuration changed during sync; skipping stale result")
	}
	if liveDigest == *digest {
		return nil
	}
	log.Printf("supersync event=local_state_changed_during_sync reason=%s", reason)
	*cfg = liveCfg
	*payload = livePayload
	*digest = liveDigest
	if pmeta != nil {
		pmeta.Dirty = pmeta.LastSyncedSHA256 != liveDigest
	}
	return nil
}

func currentLiveSyncSnapshot() (AppConfig, []byte, string, error) {
	configMu.Lock()
	cfg := cloneAppConfig(globalConfig)
	configMu.Unlock()
	payload, digest, err := marshalCanonicalSyncPayload(cfg)
	if err != nil {
		return AppConfig{}, nil, "", err
	}
	return cfg, payload, digest, nil
}

func syncReasonUsesLiveGlobalState(reason string) bool {
	switch reason {
	case "poll", "startup", "settings-save":
		return true
	default:
		return false
	}
}

func superSyncTargetChanged(a, b AppConfig) bool {
	return a.SuperSync.Enabled != b.SuperSync.Enabled ||
		strings.TrimSpace(a.SuperSync.BaseURL) != strings.TrimSpace(b.SuperSync.BaseURL) ||
		strings.TrimSpace(a.SuperSync.BucketID) != strings.TrimSpace(b.SuperSync.BucketID) ||
		strings.TrimSpace(a.SuperSync.UserID) != strings.TrimSpace(b.SuperSync.UserID) ||
		strings.TrimSpace(a.SuperSync.DeviceID) != strings.TrimSpace(b.SuperSync.DeviceID) ||
		strings.TrimSpace(a.SuperSync.ProfileID) != strings.TrimSpace(b.SuperSync.ProfileID)
}

func persistConflictArtifacts(localPayload, remotePayload []byte) error {
	if err := os.MkdirAll(superSyncConflictDir(), 0755); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	localPath := filepath.Join(superSyncConflictDir(), "settings_conflict_local_"+ts+".json")
	remotePath := filepath.Join(superSyncConflictDir(), "settings_conflict_remote_"+ts+".json")
	if err := os.WriteFile(localPath, localPayload, 0600); err != nil {
		return err
	}
	return os.WriteFile(remotePath, remotePayload, 0600)
}

func loadSuperSyncSnapshot(profileID string) ([]byte, error) {
	b, err := os.ReadFile(superSyncSnapshotPath(profileID))
	if err != nil {
		return nil, err
	}
	return b, nil
}

func saveSuperSyncSnapshot(profileID string, payload []byte) error {
	if err := os.MkdirAll(superSyncSnapshotDir(), 0755); err != nil {
		return err
	}
	path := superSyncSnapshotPath(profileID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func quarantinePayload(payload []byte) error {
	if err := os.MkdirAll(superSyncQuarantineDir(), 0755); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(superSyncQuarantineDir(), "settings_quarantine_"+ts+".json")
	return os.WriteFile(path, payload, 0600)
}

func encodePayloadForUpload(cfg AppConfig, payload []byte) ([]byte, string, string, string, error) {
	if !cfg.SuperSync.PrivateProfile {
		return payload, "", "", "", nil
	}
	if strings.TrimSpace(cfg.SuperSync.EncryptionPassphrase) == "" {
		return nil, "", "", "", errors.New("private profile requires an encryption passphrase")
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, "", "", "", err
	}
	kek, err := deriveSymmetricKey(cfg.SuperSync.EncryptionPassphrase)
	if err != nil {
		return nil, "", "", "", err
	}
	wrappedKey, wrappedNonce, err := aesGCMEncrypt(kek, dek)
	if err != nil {
		return nil, "", "", "", err
	}
	ciphertext, nonce, err := aesGCMEncrypt(dek, payload)
	if err != nil {
		return nil, "", "", "", err
	}
	return ciphertext, "AES-256-GCM", base64.StdEncoding.EncodeToString(append(wrappedNonce, wrappedKey...)), base64.StdEncoding.EncodeToString(nonce), nil
}

func decodeDownloadedPayload(cfg AppConfig, ciphertext []byte, version supersyncsdk.ItemVersionRecord) ([]byte, error) {
	if version.CipherSuite == "" {
		return ciphertext, nil
	}
	if version.CipherSuite != "AES-256-GCM" {
		return nil, fmt.Errorf("unsupported supersync cipher suite %q", version.CipherSuite)
	}
	if strings.TrimSpace(cfg.SuperSync.EncryptionPassphrase) == "" {
		return nil, errors.New("encrypted payload requires an encryption passphrase")
	}
	wrapped, err := base64.StdEncoding.DecodeString(version.WrappedKey)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < 12 {
		return nil, errors.New("wrapped key is too short")
	}
	kek, err := deriveSymmetricKey(cfg.SuperSync.EncryptionPassphrase)
	if err != nil {
		return nil, err
	}
	dek, err := aesGCMDecrypt(kek, wrapped[:12], wrapped[12:])
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(version.Nonce)
	if err != nil {
		return nil, err
	}
	return aesGCMDecrypt(dek, nonce, ciphertext)
}

func aesGCMEncrypt(key, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func aesGCMDecrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func loadSuperSyncSigningKey(keyText string) (ed25519.PrivateKey, error) {
	keyText = strings.TrimSpace(keyText)
	if keyText == "" {
		return nil, errors.New("missing supersync signing key")
	}
	if block, _ := pem.Decode([]byte(keyText)); block != nil {
		return parseEd25519PrivateKeyDER(block.Bytes)
	}

	raw, err := decodeBase64String(keyText)
	if err != nil {
		return nil, errors.New("failed to decode signing key: expected PEM or base64 Ed25519 private key")
	}
	if key, err := parseEd25519PrivateKeyDER(raw); err == nil {
		return key, nil
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("unsupported Ed25519 private key length %d", len(raw))
	}
}

func parseEd25519PrivateKeyDER(der []byte) (ed25519.PrivateKey, error) {
	priv, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := priv.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("supersync SDK requires an Ed25519 private key")
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key size")
	}
	return key, nil
}

func deriveSymmetricKey(keyText string) ([]byte, error) {
	keyText = strings.TrimSpace(keyText)
	if keyText == "" {
		return nil, errors.New("missing encryption key")
	}
	if raw, err := decodeBase64String(keyText); err == nil {
		if len(raw) == 32 {
			return raw, nil
		}
	}
	sum := sha256.Sum256([]byte(keyText))
	return sum[:], nil
}

func decodeBase64String(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return nil, errors.New("empty base64 string")
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func parseTimeLoose(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func autoResolveSettingsConflict(basePayload, localPayload, remotePayload []byte, preferRemote bool) ([]byte, string, error) {
	if len(basePayload) == 0 {
		if preferRemote {
			return remotePayload, "remote_wins_no_base", nil
		}
		return localPayload, "local_wins_no_base", nil
	}
	var baseCfg, localCfg, remoteCfg AppConfig
	if err := json.Unmarshal(basePayload, &baseCfg); err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal(localPayload, &localCfg); err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal(remotePayload, &remoteCfg); err != nil {
		return nil, "", err
	}
	merged := mergeAppConfig(baseCfg, localCfg, remoteCfg, preferRemote)
	out, _, err := marshalCanonicalSyncPayload(merged)
	if err != nil {
		return nil, "", err
	}
	return out, "three_way_merge", nil
}

func mergeAppConfig(base, local, remote AppConfig, preferRemote bool) AppConfig {
	merged := local
	merged.NoWindowsTitles = chooseComparable(base.NoWindowsTitles, local.NoWindowsTitles, remote.NoWindowsTitles, preferRemote)
	merged.SnapEnabled = chooseComparable(base.SnapEnabled, local.SnapEnabled, remote.SnapEnabled, preferRemote)
	merged.AlwaysOnTopAll = chooseComparable(base.AlwaysOnTopAll, local.AlwaysOnTopAll, remote.AlwaysOnTopAll, preferRemote)
	merged.ActiveOnTray = chooseComparable(base.ActiveOnTray, local.ActiveOnTray, remote.ActiveOnTray, preferRemote)
	merged.ActiveOnWin = chooseComparable(base.ActiveOnWin, local.ActiveOnWin, remote.ActiveOnWin, preferRemote)
	merged.LimitGuiRefresh = chooseComparable(base.LimitGuiRefresh, local.LimitGuiRefresh, remote.LimitGuiRefresh, preferRemote)
	merged.GuiRefreshMs = chooseComparable(base.GuiRefreshMs, local.GuiRefreshMs, remote.GuiRefreshMs, preferRemote)
	merged.RepaintOnNewFrame = chooseComparable(base.RepaintOnNewFrame, local.RepaintOnNewFrame, remote.RepaintOnNewFrame, preferRemote)
	merged.HealthChip = chooseComparable(base.HealthChip, local.HealthChip, remote.HealthChip, preferRemote)
	merged.ShowFPS = chooseComparable(base.ShowFPS, local.ShowFPS, remote.ShowFPS, preferRemote)
	merged.ShowBitrate = chooseComparable(base.ShowBitrate, local.ShowBitrate, remote.ShowBitrate, preferRemote)
	merged.ShowDrops = chooseComparable(base.ShowDrops, local.ShowDrops, remote.ShowDrops, preferRemote)
	merged.ShowCPUUsage = chooseComparable(base.ShowCPUUsage, local.ShowCPUUsage, remote.ShowCPUUsage, preferRemote)
	merged.SuperSync = mergeSuperSyncConfig(base.SuperSync, local.SuperSync, remote.SuperSync, preferRemote)
	merged.Cameras = mergeCameraConfigs(base.Cameras, local.Cameras, remote.Cameras, preferRemote)
	return merged
}

func mergeSuperSyncConfig(base, local, remote SuperSyncConfig, preferRemote bool) SuperSyncConfig {
	merged := local
	merged.Enabled = chooseComparable(base.Enabled, local.Enabled, remote.Enabled, preferRemote)
	merged.BaseURL = chooseComparable(base.BaseURL, local.BaseURL, remote.BaseURL, preferRemote)
	merged.AppID = chooseComparable(base.AppID, local.AppID, remote.AppID, preferRemote)
	merged.BucketID = chooseComparable(base.BucketID, local.BucketID, remote.BucketID, preferRemote)
	merged.UserID = chooseComparable(base.UserID, local.UserID, remote.UserID, preferRemote)
	merged.ProfileID = chooseComparable(base.ProfileID, local.ProfileID, remote.ProfileID, preferRemote)
	merged.PrivateProfile = chooseComparable(base.PrivateProfile, local.PrivateProfile, remote.PrivateProfile, preferRemote)
	merged.SharedProfile = chooseComparable(base.SharedProfile, local.SharedProfile, remote.SharedProfile, preferRemote)
	merged.SyncOnStartup = chooseComparable(base.SyncOnStartup, local.SyncOnStartup, remote.SyncOnStartup, preferRemote)
	merged.SyncOnSave = chooseComparable(base.SyncOnSave, local.SyncOnSave, remote.SyncOnSave, preferRemote)
	merged.AllowPullApply = chooseComparable(base.AllowPullApply, local.AllowPullApply, remote.AllowPullApply, preferRemote)
	merged.ShareRecipients = mergeStringSlices(base.ShareRecipients, local.ShareRecipients, remote.ShareRecipients, preferRemote)
	return merged
}

func mergeCameraConfigs(base, local, remote []CameraConfig, preferRemote bool) []CameraConfig {
	baseMap := mapByCameraKey(base)
	localMap := mapByCameraKey(local)
	remoteMap := mapByCameraKey(remote)

	order := make([]string, 0, len(local)+len(remote)+len(base))
	appendOrder := func(list []CameraConfig) {
		for _, c := range list {
			key := cameraRuntimeKey(c)
			found := false
			for _, existing := range order {
				if existing == key {
					found = true
					break
				}
			}
			if !found {
				order = append(order, key)
			}
		}
	}
	appendOrder(local)
	appendOrder(remote)
	appendOrder(base)

	out := make([]CameraConfig, 0, len(order))
	for _, key := range order {
		baseCam, baseOK := baseMap[key]
		localCam, localOK := localMap[key]
		remoteCam, remoteOK := remoteMap[key]
		mergedCam, keep := mergeOneCamera(baseCam, baseOK, localCam, localOK, remoteCam, remoteOK, preferRemote)
		if keep {
			out = append(out, mergedCam)
		}
	}
	return out
}

func mergeOneCamera(base CameraConfig, baseOK bool, local CameraConfig, localOK bool, remote CameraConfig, remoteOK bool, preferRemote bool) (CameraConfig, bool) {
	switch {
	case !baseOK && localOK && remoteOK:
		if cameraSyncEquivalent(local, remote) {
			return local, true
		}
		if preferRemote {
			return remote, true
		}
		return local, true
	case !baseOK && localOK:
		return local, true
	case !baseOK && remoteOK:
		return remote, true
	case baseOK && !localOK && !remoteOK:
		return CameraConfig{}, false
	case baseOK && !localOK && remoteOK:
		if cameraSyncEquivalent(base, remote) {
			return CameraConfig{}, false
		}
		if preferRemote {
			return remote, true
		}
		return CameraConfig{}, false
	case baseOK && localOK && !remoteOK:
		if cameraSyncEquivalent(base, local) {
			return CameraConfig{}, false
		}
		if preferRemote {
			return CameraConfig{}, false
		}
		return local, true
	}

	merged := local
	merged.ID = chooseComparable(base.ID, local.ID, remote.ID, preferRemote)
	merged.Name = chooseComparable(base.Name, local.Name, remote.Name, preferRemote)
	merged.Disabled = chooseComparable(base.Disabled, local.Disabled, remote.Disabled, preferRemote)
	merged.URL = chooseComparable(base.URL, local.URL, remote.URL, preferRemote)
	merged.RTSPTCP = chooseComparable(base.RTSPTCP, local.RTSPTCP, remote.RTSPTCP, preferRemote)
	merged.Caching = chooseComparable(base.Caching, local.Caching, remote.Caching, preferRemote)
	merged.AlwaysOnTop = chooseComparable(base.AlwaysOnTop, local.AlwaysOnTop, remote.AlwaysOnTop, preferRemote)
	merged.Mute = chooseComparable(base.Mute, local.Mute, remote.Mute, preferRemote)
	merged.Stretch = chooseComparable(base.Stretch, local.Stretch, remote.Stretch, preferRemote)
	merged.FFmpegParams = chooseComparable(base.FFmpegParams, local.FFmpegParams, remote.FFmpegParams, preferRemote)
	merged.Probesize = chooseComparable(base.Probesize, local.Probesize, remote.Probesize, preferRemote)
	merged.AnalyzeUS = chooseComparable(base.AnalyzeUS, local.AnalyzeUS, remote.AnalyzeUS, preferRemote)
	merged.Threads = chooseComparable(base.Threads, local.Threads, remote.Threads, preferRemote)
	merged.HwAccel = chooseComparable(base.HwAccel, local.HwAccel, remote.HwAccel, preferRemote)
	merged.Volume = chooseIntPtr(base.Volume, local.Volume, remote.Volume, preferRemote)
	return merged, true
}

func mapByCameraKey(list []CameraConfig) map[string]CameraConfig {
	out := make(map[string]CameraConfig, len(list))
	for _, c := range list {
		out[cameraRuntimeKey(c)] = c
	}
	return out
}

func mergeStringSlices(base, local, remote []string, preferRemote bool) []string {
	b := append([]string(nil), base...)
	l := append([]string(nil), local...)
	r := append([]string(nil), remote...)
	slices.Sort(b)
	slices.Sort(l)
	slices.Sort(r)
	if slices.Equal(l, r) {
		return local
	}
	if slices.Equal(l, b) {
		return remote
	}
	if slices.Equal(r, b) {
		return local
	}
	if preferRemote {
		return remote
	}
	return local
}

func chooseIntPtr(base, local, remote *int, preferRemote bool) *int {
	if intPtrEqual(local, remote) {
		return cloneIntPtr(local)
	}
	if intPtrEqual(local, base) {
		return cloneIntPtr(remote)
	}
	if intPtrEqual(remote, base) {
		return cloneIntPtr(local)
	}
	if preferRemote {
		return cloneIntPtr(remote)
	}
	return cloneIntPtr(local)
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func chooseComparable[T comparable](base, local, remote T, preferRemote bool) T {
	if local == remote {
		return local
	}
	if local == base {
		return remote
	}
	if remote == base {
		return local
	}
	if preferRemote {
		return remote
	}
	return local
}
