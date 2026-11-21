package update

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type device struct{ base string }

func (d device) file(name string) string { return filepath.Join(d.base, "updates", name) }
func (d device) binary() string          { return filepath.Join(d.base, "bin", "ncc") }

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 update public key")
	}
	return ed25519.PublicKey(key), nil
}

func CheckKey(path string) error {
	_, err := loadPublicKey(path)
	return err
}

func (d device) listen() (*net.UnixListener, error) {
	var owner unix.Stat_t
	if err := unix.Stat(filepath.Join(d.base, "state"), &owner); err != nil || owner.Uid == 0 {
		return nil, errors.New("cannot identify the non-root Web service owner")
	}
	if err := os.Remove(Socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: Socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chown(Socket, int(owner.Uid), int(owner.Gid)); err == nil {
		err = os.Chmod(Socket, 0600)
	}
	if err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func fetchChild(ctx context.Context, wifiOnly bool, address string, limit int64, out io.Writer) error {
	if !validURL(address, false) || limit < 1 || limit > maxAsset {
		return errors.New("更新ファイルを取得できません")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "--mode", "update-fetch", "--update-url", address, "--update-limit", strconv.FormatInt(limit, 10), "--update-wifi="+strconv.FormatBool(wifiOnly))
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	if wifiOnly {
		cmd.SysProcAttr.AmbientCaps = []uintptr{unix.CAP_NET_RAW}
	}
	cmd.Stdout = out
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), errNoWiFi.Error()) {
			return errNoWiFi
		}
		log.Printf("GitHub update fetch: %s", strings.TrimSpace(stderr.String()))
		return errors.New("GitHubから更新情報またはファイルを取得できません")
	}
	return nil
}

func Fetch(ctx context.Context, wifiOnly bool, address string, limit int64, out io.Writer) error {
	if os.Geteuid() == 0 || limit < 1 || limit > maxAsset {
		return errors.New("invalid update fetch invocation")
	}
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	if wifiOnly {
		if iface, err := net.InterfaceByName("wlan0"); err != nil || iface.Flags&net.FlagUp == 0 {
			return errNoWiFi
		}
		bind := func(_, _ string, socket syscall.RawConn) error {
			var bindErr error
			if err := socket.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), "wlan0") }); err != nil {
				return err
			}
			return bindErr
		}
		dnsDialer := &net.Dialer{Timeout: 8 * time.Second, Control: bind}
		dialer.Control = bind
		dialer.Resolver = &net.Resolver{PreferGo: true, Dial: dnsDialer.DialContext}
	}
	client := &http.Client{
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 12 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DisableCompression: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 4 || !validURL(req.URL.String(), true) {
				return errors.New("invalid GitHub redirect")
			}
			return nil
		},
	}
	err := downloadHTTP(ctx, client, address, limit, out)
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if wifiOnly {
			return errNoWiFi
		}
		return errors.New("GitHubへのネットワーク接続に失敗しました")
	}
	return err
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func durableFile(path string, mode os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func durableJSON(path string, value any) error {
	return durableFile(path, 0600, func(out io.Writer) error { return json.NewEncoder(out).Encode(value) })
}

func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 200<<10)).Decode(value)
}

func regularFile(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("unexpected update file")
	}
	return os.Open(path)
}

func fileHash(path string, limit int64) (string, error) {
	f, err := regularFile(path, limit)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, limit+1))
	if n > limit {
		return "", errors.New("updated file is too large")
	}
	return hex.EncodeToString(hash.Sum(nil)), err
}

func (d device) stage(ctx context.Context, fetch downloadFunc, wifiOnly bool, item candidate) error {
	m := item.meta
	var disk unix.Statfs_t
	required := m.AssetSize + 2*m.BinarySize + maxBinary + (8 << 20)
	if err := unix.Statfs(d.file("."), &disk); err != nil || disk.Bavail < uint64((required+int64(disk.Bsize)-1)/int64(disk.Bsize)) {
		return errors.New("本体の更新用空き容量が足りません")
	}
	compressed, err := os.CreateTemp(d.file("."), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(compressed.Name())
	defer compressed.Close()
	hash := sha256.New()
	if err := fetch(ctx, wifiOnly, item.url, m.AssetSize, io.MultiWriter(compressed, hash)); err != nil {
		return err
	}
	size, err := compressed.Seek(0, io.SeekCurrent)
	if err != nil || size != m.AssetSize || hex.EncodeToString(hash.Sum(nil)) != m.AssetSHA {
		return errors.New("ダウンロードしたファイルのサイズまたはハッシュが一致しません")
	}
	if _, err := compressed.Seek(0, io.SeekStart); err != nil {
		return err
	}
	archive, err := gzip.NewReader(compressed)
	if err != nil {
		return errors.New("更新パッケージを展開できません")
	}
	defer archive.Close()
	return durableFile(d.file("candidate.ncc"), 0700, func(out io.Writer) error {
		hash.Reset()
		n, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(archive, m.BinarySize+1))
		if err != nil || n != m.BinarySize || hex.EncodeToString(hash.Sum(nil)) != m.BinarySHA {
			return errors.New("展開したバイナリのサイズまたはハッシュが一致しません")
		}
		return nil
	})
}

var errUpdateBusy = errors.New("別の配備・復旧処理を実行中です")
var errUpdateNotReady = errors.New("純正サービスの起動完了後に更新します")

var stockUnits = []string{"app-@Communication.service", "app-@DriveRec.service", "app-@ImageRecog.service", "app-@Voice.service", "qmmf-server.service"}

func (d device) lock() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(d.base, "recovery", "ncc-update.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errUpdateBusy
	}
	return f, nil
}

type transaction struct {
	PreviousSHA  string `json:"previousSha"`
	CandidateSHA string `json:"candidateSha"`
	LEDWasActive bool   `json:"ledWasActive"`
}

func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func binaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := command(ctx, path, "--mode", "update-version")
	version := strings.TrimSpace(string(out))
	if err != nil || !stableTag.MatchString("v"+version) {
		return "", errors.New("導入済みのNP1CCバージョンを確認できません")
	}
	return version, nil
}

// The NP1 mounts owner storage noexec, so only inspect images copied to /run.
func (d device) installedVersion(ctx context.Context) (string, error) {
	work, err := os.MkdirTemp(filepath.Dir(Socket), ".version-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	image := filepath.Join(work, "ncc")
	if err := copyBinary(d.binary(), image, 0700); err != nil {
		return "", err
	}
	return binaryVersion(ctx, image)
}

func copyBinary(from, to string, mode os.FileMode) error {
	f, err := regularFile(from, maxBinary)
	if err != nil {
		return err
	}
	defer f.Close()
	return durableFile(to, mode, func(out io.Writer) error { _, err := io.Copy(out, f); return err })
}

func captureUnits(ctx context.Context) ([]byte, error) {
	args := []string{"show", "-p", "Id", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState"}
	args = append(args, stockUnits...)
	args = append(args, "np1-control-ap.service", "np1-control-modem.service", "np1-control-native.service")
	return command(ctx, "systemctl", args...)
}

func (d device) restartAndVerify(ctx context.Context, expected string, led bool) error {
	for _, unit := range []string{"np1-control-broker.service", "np1-control.service", "np1-control-ota-guard.service"} {
		if out, err := command(ctx, "systemctl", "restart", unit); err != nil {
			return fmt.Errorf("%s: %w: %s", unit, err, out)
		}
	}
	if led {
		if out, err := command(ctx, "systemctl", "start", "np1-control-led.service"); err != nil {
			return fmt.Errorf("LED: %w: %s", err, out)
		}
	}
	if out, err := command(ctx, "/bin/sh", "-c", `cd "$1" && exec /bin/sh ./verify.sh`, "update-verify", d.file(".")); err != nil {
		return fmt.Errorf("read-back: %w: %s", err, out)
	}
	for _, path := range []string{"/run/np1-control/ncc", "/run/np1-control-ota-guard/ncc"} {
		if hash, err := fileHash(path, maxBinary); err != nil || hash != expected {
			return errors.New("running NCC binary read-back mismatch")
		}
	}
	return nil
}

func (d device) apply(ctx context.Context, m manifest) error {
	lock, err := d.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Stat(d.file("transaction.json")); !os.IsNotExist(err) {
		return errors.New("前回の更新の復旧が必要です")
	}
	oldHash, err := fileHash(d.binary(), maxBinary)
	if err != nil || !slices.Contains(m.From, oldHash) {
		return errors.New("適用直前に更新元のバイナリが変わりました")
	}
	staged := d.file("candidate.ncc")
	if hash, err := fileHash(staged, maxBinary); err != nil || hash != m.BinarySHA {
		return errors.New("保存済み更新ファイルを検証できません")
	}
	preflight, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, unit := range stockUnits {
		if _, err := command(preflight, "systemctl", "is-active", "--quiet", unit); err != nil {
			return errUpdateNotReady
		}
	}
	work, err := os.MkdirTemp(filepath.Dir(Socket), ".check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	oldImage, newImage := filepath.Join(work, "old"), filepath.Join(work, "new")
	if err := copyBinary(d.binary(), oldImage, 0700); err != nil {
		return err
	}
	if err := copyBinary(staged, newImage, 0700); err != nil {
		return err
	}
	if version, err := binaryVersion(preflight, newImage); err != nil || version != m.Version {
		return errors.New("更新バイナリのバージョンが一致しません")
	}
	if _, err := command(preflight, newImage, "--mode", "self-test"); err != nil {
		return errors.New("更新バイナリの自己診断に失敗しました")
	}
	oldContract, oldErr := command(preflight, oldImage, "--mode", "update-recorder-contract")
	newContract, newErr := command(preflight, newImage, "--mode", "update-recorder-contract")
	if oldErr != nil || newErr != nil || !slices.Equal(oldContract, newContract) ||
		!strings.HasPrefix(string(newContract), m.Hook+" "+m.Injector+" ") {
		return errors.New("更新バイナリの録画連携契約が導入済みの版と一致しません")
	}
	before, err := captureUnits(preflight)
	if err != nil {
		return errors.New("更新前のサービス状態を確認できません")
	}
	_, ledErr := command(preflight, "systemctl", "is-active", "--quiet", "np1-control-led.service")
	txn := transaction{PreviousSHA: oldHash, CandidateSHA: m.BinarySHA, LEDWasActive: ledErr == nil}
	if err := copyBinary(d.binary(), d.file("previous.ncc"), 0700); err != nil {
		return errors.New("更新前のバイナリをバックアップできません")
	}
	if err := durableJSON(d.file("transaction.json"), txn); err != nil {
		return errors.New("更新の復旧情報を保存できません")
	}
	applyCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
	defer stop()
	if err := d.switchAndVerify(applyCtx, m, before, txn.LEDWasActive); err != nil {
		log.Printf("verifying NP1CC update: %v", err)
		recovery, stop := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stop()
		if err := d.restore(recovery, txn, true); err != nil {
			log.Printf("restoring NP1CC update: %v", err)
			return errors.New("更新の復旧に失敗しました。ADBから復旧を確認してください")
		}
		return errors.New("更新後の検証に失敗したため直前の版に戻しました")
	}
	return d.clearTransaction()
}

func (d device) switchAndVerify(ctx context.Context, m manifest, before []byte, led bool) error {
	if err := copyBinary(d.file("candidate.ncc"), d.binary(), 0755); err != nil {
		return err
	}
	if err := d.restartAndVerify(ctx, m.BinarySHA, led); err != nil {
		return err
	}
	after, err := captureUnits(ctx)
	if err != nil || !slices.Equal(before, after) {
		return errors.New("stock or owner worker identity changed during update")
	}
	return nil
}

func (d device) clearTransaction() error {
	if err := os.Remove(d.file("transaction.json")); err != nil {
		return err
	}
	return syncDir(d.file("."))
}

func (d device) restore(ctx context.Context, txn transaction, restart bool) error {
	if hash, err := fileHash(d.file("previous.ncc"), maxBinary); err != nil || hash != txn.PreviousSHA {
		return errors.New("recovery backup hash mismatch")
	}
	if err := copyBinary(d.file("previous.ncc"), d.binary(), 0755); err != nil {
		return err
	}
	if restart {
		if err := d.restartAndVerify(ctx, txn.PreviousSHA, txn.LEDWasActive); err != nil {
			return err
		}
	}
	return d.clearTransaction()
}

func (d device) recover(ctx context.Context, restart bool) error {
	var txn transaction
	err := readJSON(d.file("transaction.json"), &txn)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !hashValid(txn.PreviousSHA) || !hashValid(txn.CandidateSHA) {
		return errors.New("invalid pending NP1CC update transaction")
	}
	lock, err := d.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	var s state
	s.Settings.Mode = modeCheck
	if err := readJSON(d.file("state.json"), &s); err != nil && !os.IsNotExist(err) {
		log.Printf("reading updater state during recovery: %v", err)
		s = state{Settings: settings{Mode: modeCheck}}
	}
	if err := d.restore(ctx, txn, restart); err != nil {
		return err
	}
	s.Result, s.Message = "recovered", "中断された更新から直前の版に復旧しました"
	s.Pending, s.FailedSHA = nil, txn.CandidateSHA
	_ = os.Remove(d.file("candidate.ncc"))
	return durableJSON(d.file("state.json"), s)
}

func Recover(ctx context.Context) error { return (device{base: baseDir}).recover(ctx, false) }
