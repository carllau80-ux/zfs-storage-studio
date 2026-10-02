package app

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config 是 Manager 的 TOML 配置(/etc/zfs-platform/manager.toml)。
// 敏感项只写引用:*_file 指向 0600 文件;也接受 *_env。
type Config struct {
	Listen         string `toml:"listen"`      // HTTP 监听(基线:仅回环,由 Caddy/nginx 反代)
	DBURLFile      string `toml:"db_url_file"` // PG DSN 文件(0600)
	DBURLEnv       string `toml:"db_url_env"`  // 或环境变量名
	AgentTokenFile string `toml:"agent_token_file"`
	DataKeyFile    string `toml:"data_key_file"`
	PollSeconds    int    `toml:"poll_seconds"`
	StaticDir      string `toml:"static_dir"`

	// 解析后(不落盘、不打印)
	dbURL      string
	agentToken string
	dataKeyHex string
}

const (
	defaultListen = "127.0.0.1:8080" // 基线:应用不监听明文对外端口
)

func readSecretFile(path, what string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s 文件不可读(%s): %w", what, path, err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s 文件权限过宽(%s 现为 %04o),应为 0600", what, path, st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s 文件为空: %s", what, path)
	}
	return v, nil
}

// LoadConfig 读取 TOML;若仅存在旧版 manager.json 则一次性迁移(密钥转 0600 文件)。
func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			legacy := strings.TrimSuffix(path, ".toml") + ".json"
			if _, e2 := os.Stat(legacy); e2 == nil {
				if err := migrateLegacyConfig(legacy, path); err != nil {
					return nil, fmt.Errorf("旧配置迁移失败: %w", err)
				}
			} else {
				return nil, fmt.Errorf("配置文件不存在: %s", path)
			}
		} else {
			return nil, err
		}
	}
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("解析 TOML 失败: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("配置存在未知键(已拒绝启动): %s", strings.Join(keys, ", "))
	}
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.PollSeconds <= 0 {
		c.PollSeconds = 12
	}
	// 敏感项解析:file 优先,其次 env;两者都缺则启动失败(fail-fast)
	if c.DBURLFile != "" {
		if c.dbURL, err = readSecretFile(c.DBURLFile, "db_url"); err != nil {
			return nil, err
		}
	} else if c.DBURLEnv != "" {
		c.dbURL = os.Getenv(c.DBURLEnv)
	}
	if c.dbURL == "" {
		return nil, fmt.Errorf("缺少数据库连接:请配置 db_url_file(推荐)或 db_url_env")
	}
	if c.AgentTokenFile != "" {
		if c.agentToken, err = readSecretFile(c.AgentTokenFile, "agent_token"); err != nil {
			return nil, err
		}
	}
	if c.agentToken == "" {
		return nil, fmt.Errorf("缺少 agent_token:请配置 agent_token_file")
	}
	if c.DataKeyFile != "" {
		if c.dataKeyHex, err = readSecretFile(c.DataKeyFile, "data_key"); err != nil {
			return nil, err
		}
	}
	if len(c.dataKeyHex) != 64 {
		return nil, fmt.Errorf("data_key 须为 64 位 hex(32 字节)")
	}
	return &c, nil
}

// migrateLegacyConfig 把旧 manager.json 转为 TOML + 0600 密钥文件(幂等,保留 .bak)。
func migrateLegacyConfig(legacyPath, tomlPath string) error {
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}
	var m struct {
		Listen      string `json:"listen"`
		DBURL       string `json:"db_url"`
		AgentToken  string `json:"agent_token"`
		DataKeyHex  string `json:"data_key_hex"`
		PollSeconds int    `json:"poll_seconds"`
		StaticDir   string `json:"static_dir"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	dir := filepath.Dir(tomlPath)
	writeSecret := func(name, val string) (string, error) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(val+"\n"), 0o600); err != nil {
			return "", err
		}
		return p, os.Chmod(p, 0o600)
	}
	dbFile, err := writeSecret("pg.dsn", m.DBURL)
	if err != nil {
		return err
	}
	tokFile, err := writeSecret("agent.token", m.AgentToken)
	if err != nil {
		return err
	}
	keyFile, err := writeSecret("data.key", m.DataKeyHex)
	if err != nil {
		return err
	}
	listen := m.Listen
	if listen == "" || strings.HasPrefix(listen, ":") || strings.HasPrefix(listen, "0.0.0.0") {
		listen = defaultListen // 迁移即按基线收敛为回环
	}
	tomlBody := fmt.Sprintf(`# RunStor Manager 配置(由旧 manager.json 迁移生成 %s)
listen = %q
db_url_file = %q
agent_token_file = %q
data_key_file = %q
poll_seconds = %d
static_dir = %q
`, "2026-09-23", listen, dbFile, tokFile, keyFile,
		func() int {
			if m.PollSeconds > 0 {
				return m.PollSeconds
			}
			return 12
		}(), m.StaticDir)
	if err := os.WriteFile(tomlPath, []byte(tomlBody), 0o600); err != nil {
		return err
	}
	_ = os.Rename(legacyPath, legacyPath+".bak")
	return nil
}

// DataKey 返回 AES-256 密钥。
func (c *Config) DataKey() ([]byte, error) {
	if len(c.dataKeyHex) != 64 {
		return nil, fmt.Errorf("data_key_hex 应为 64 位 hex")
	}
	return hex.DecodeString(c.dataKeyHex)
}

func (c *Config) DBURL() string      { return c.dbURL }
func (c *Config) AgentToken() string { return c.agentToken }
