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
)

var superSync = newSuperSyncManager()

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
	cfg := globalConfig
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

	if remoteItem == nil {
		if !localDirty {
			now := time.Now().UTC()
			m.updateStatus("sync_success", now, "")
			return nil
		}
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

	remoteChanged := remoteVersion != 0 && (remoteVersion != pmeta.LastSyncedVersion || remoteDigest != pmeta.LastSyncedSHA256)
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
		m.updateStatus("pull_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=pull_applied profile=%s size=%d", profileID, len(remotePayload))
		return nil
	}

	if localDirty {
		if remoteChanged {
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
		if err := reconcileShares(client, cfg, upload.Item.ID); err != nil {
			m.updateStatus("sync_failure", time.Time{}, err.Error())
			return err
		}
		m.updateStatus("push_applied", pmeta.LastSyncAt, "")
		log.Printf("supersync event=push_applied profile=%s size=%d", profileID, len(payload))
	}

	return nil
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

func superSyncItemKey(profileID string) string {
	if profileID == "" || profileID == "default" {
		return "settings/default"
	}
	return "settings/profiles/" + sanitizeFSComponent(profileID)
}

func marshalCanonicalSyncPayload(cfg AppConfig) ([]byte, string, error) {
	syncCfg := cfg
	ensureCameraIDs(syncCfg.Cameras)
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
	remoteCfg.SuperSync.LastSyncStatus = local.SuperSync.LastSyncStatus
	remoteCfg.SuperSync.LastSyncAt = local.SuperSync.LastSyncAt
	remoteCfg.SuperSync.LastError = local.SuperSync.LastError
	ensureSuperSyncDefaults(&remoteCfg)
	ensureCameraIDs(remoteCfg.Cameras)

	configMu.Lock()
	globalConfig = remoteCfg
	configMu.Unlock()
	return SaveConfig()
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
