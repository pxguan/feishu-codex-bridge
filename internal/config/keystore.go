package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

// keystore.go —— 本地 AES-256-GCM 密钥库（字节级复刻 TS config/keystore，见方案 §2/§4）。
//
//   ~/.feishu-codex-bridge/secrets.enc     — JSON {version:1, entries:{id:{iv,data,tag}}}
//   ~/.feishu-codex-bridge/.keystore.salt  — 32 随机字节（一次性生成）
//   两者均 0600。密钥 = PBKDF2-SHA256(100k, seed=hostname|username, salt) → 32B。
//
// 与 TS 互通要点：Go cipher.GCM.Seal/Open 把密文与 tag 拼接（tag 在末尾 16B），
// 而 TS 分开存 data/tag。故加密切末 16B 为 tag、解密前拼回 data||tag。
//
// 这是纵深防御（防 backup/git/log 泄露），不防同用户进程。

const (
	ksKeyLen     = 32
	ksIVLen      = 12
	ksTagLen     = 16
	ksPBKDF2Iter = 100_000
	ksVersion    = 1
)

type ksEnvelope struct {
	IV   string `json:"iv"`
	Data string `json:"data"`
	Tag  string `json:"tag"`
}

type ksStoreFile struct {
	Version int                   `json:"version"`
	Entries map[string]ksEnvelope `json:"entries"`
}

// Keystore 本地 AES-256-GCM 密钥库。
type Keystore struct {
	secretsFile string
	saltFile    string
	seed        string

	mu sync.Mutex // 串行化 Set/Remove 的 read-modify-write
}

// NewKeystore 用给定密钥库文件与 salt 文件构造；seed 默认 KeystoreSeed()。
func NewKeystore(secretsFile, saltFile string) *Keystore {
	return &Keystore{
		secretsFile: secretsFile,
		saltFile:    saltFile,
		seed:        KeystoreSeed(),
	}
}

// WithSeed 注入 seed（测试用），返回 k 自身便于链式。
func (k *Keystore) WithSeed(seed string) *Keystore {
	k.seed = seed
	return k
}

// Get 取出 id 对应明文；不存在返回 ("", false, nil)。
func (k *Keystore) Get(id string) (string, bool, error) {
	store, err := k.readStore()
	if err != nil {
		return "", false, err
	}
	env, ok := store.Entries[id]
	if !ok {
		return "", false, nil
	}
	key, err := k.deriveKey()
	if err != nil {
		return "", false, err
	}
	pt, err := ksDecrypt(key, env)
	if err != nil {
		return "", false, err
	}
	return pt, true, nil
}

// Set 加密并写入 id。
func (k *Keystore) Set(id, plaintext string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, err := k.deriveKey()
	if err != nil {
		return err
	}
	env, err := ksEncrypt(key, plaintext)
	if err != nil {
		return err
	}
	store, err := k.readStore()
	if err != nil {
		return err
	}
	if store.Entries == nil {
		store.Entries = map[string]ksEnvelope{}
	}
	store.Entries[id] = env
	return k.writeStore(store)
}

// Remove 删除 id；返回是否确实删除了一条。
func (k *Keystore) Remove(id string) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	store, err := k.readStore()
	if err != nil {
		return false, err
	}
	if _, ok := store.Entries[id]; !ok {
		return false, nil
	}
	delete(store.Entries, id)
	if err := k.writeStore(store); err != nil {
		return false, err
	}
	return true, nil
}

// List 返回全部 id（升序）。
func (k *Keystore) List() ([]string, error) {
	store, err := k.readStore()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(store.Entries))
	for id := range store.Entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (k *Keystore) readStore() (ksStoreFile, error) {
	b, err := os.ReadFile(k.secretsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return ksStoreFile{Version: ksVersion, Entries: map[string]ksEnvelope{}}, nil
		}
		return ksStoreFile{}, err
	}
	var s ksStoreFile
	if err := json.Unmarshal(b, &s); err != nil {
		return ksStoreFile{}, err
	}
	if s.Version != ksVersion || s.Entries == nil {
		return ksStoreFile{Version: ksVersion, Entries: map[string]ksEnvelope{}}, nil
	}
	return s, nil
}

func (k *Keystore) writeStore(s ksStoreFile) error {
	if err := os.MkdirAll(filepath.Dir(k.secretsFile), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := fmt.Sprintf("%s.tmp-%d", k.secretsFile, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.secretsFile)
}

func (k *Keystore) deriveKey() ([]byte, error) {
	salt, err := k.loadOrCreateSalt()
	if err != nil {
		return nil, err
	}
	return pbkdf2.Key([]byte(k.seed), salt, ksPBKDF2Iter, ksKeyLen, sha256.New), nil
}

func (k *Keystore) loadOrCreateSalt() ([]byte, error) {
	if b, err := os.ReadFile(k.saltFile); err == nil && len(b) == ksKeyLen {
		return b, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	salt := make([]byte, ksKeyLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(k.saltFile), 0o755); err != nil {
		return nil, err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", k.saltFile, os.Getpid())
	if err := os.WriteFile(tmp, salt, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, k.saltFile); err != nil {
		return nil, err
	}
	return salt, nil
}

func ksEncrypt(key []byte, plaintext string) (ksEnvelope, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return ksEnvelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ksEnvelope{}, err
	}
	iv := make([]byte, ksIVLen)
	if _, err := rand.Read(iv); err != nil {
		return ksEnvelope{}, err
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil) // ciphertext || tag(16B 末尾)
	data := sealed[:len(sealed)-ksTagLen]
	tag := sealed[len(sealed)-ksTagLen:]
	return ksEnvelope{
		IV:   base64.StdEncoding.EncodeToString(iv),
		Data: base64.StdEncoding.EncodeToString(data),
		Tag:  base64.StdEncoding.EncodeToString(tag),
	}, nil
}

func ksDecrypt(key []byte, env ksEnvelope) (string, error) {
	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", err
	}
	tag, err := base64.StdEncoding.DecodeString(env.Tag)
	if err != nil {
		return "", err
	}
	if len(iv) != ksIVLen {
		return "", errors.New("invalid IV length")
	}
	if len(tag) != ksTagLen {
		return "", errors.New("invalid auth tag length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := make([]byte, 0, len(data)+len(tag))
	sealed = append(sealed, data...)
	sealed = append(sealed, tag...)
	pt, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
