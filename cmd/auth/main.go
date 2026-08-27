// Command auth 提供 EnvVault 本地认证密钥和密码哈希离线工具
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	infraauth "env-vault/internal/infrastructure/auth"
)

const defaultPasswordEnv = "ENV_VAULT_PASSWORD"

type lookupEnvFunc func(string) (string, bool)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv); err != nil {
		_, _ = io.WriteString(os.Stderr, "错误："+err.Error()+"\n")
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer, lookupEnv lookupEnvFunc) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		writeUsage(stdout)
		return nil
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "hash-password":
		return runHashPassword(args[1:], stdout, stderr, lookupEnv)
	default:
		writeUsage(stderr)
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func runKeygen(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outDir := flags.String("out-dir", ".local/auth", "密钥文件输出目录")
	bits := flags.Int("bits", 3072, "RSA 密钥位数，最小 2048")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("keygen 不接受位置参数")
	}
	dir := filepath.Clean(strings.TrimSpace(*outDir))
	if dir == "" || dir == "." || filepath.VolumeName(dir)+string(filepath.Separator) == dir {
		return errors.New("out-dir 必须是专用密钥目录")
	}
	privatePEM, publicPEM, err := infraauth.GenerateRSAKeyPair(*bits)
	if err != nil {
		return err
	}
	defer clear(privatePEM)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建密钥目录: %w", err)
	}
	privatePath := filepath.Join(dir, "jwt-private.pem")
	publicPath := filepath.Join(dir, "jwt-public.pem")
	if err := writeExclusive(privatePath, privatePEM, 0o600); err != nil {
		return err
	}
	if err := writeExclusive(publicPath, publicPEM, 0o644); err != nil {
		_ = os.Remove(privatePath)
		return err
	}
	_, err = fmt.Fprintf(stdout, "JWT 密钥已生成\n私钥: %s\n公钥: %s\n", privatePath, publicPath)
	return err
}

func runHashPassword(args []string, stdout, stderr io.Writer, lookupEnv lookupEnvFunc) error {
	flags := flag.NewFlagSet("hash-password", flag.ContinueOnError)
	flags.SetOutput(stderr)
	passwordEnv := flags.String("password-env", defaultPasswordEnv, "保存待哈希密码的环境变量名")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("hash-password 不接受位置参数，密码只能通过环境变量传入")
	}
	envName := strings.TrimSpace(*passwordEnv)
	if envName == "" || strings.Contains(envName, "=") {
		return errors.New("password-env 必须是有效的环境变量名")
	}
	password, exists := lookupEnv(envName)
	if !exists || password == "" {
		return fmt.Errorf("环境变量 %s 未设置或为空", envName)
	}
	hasher, err := infraauth.NewPasswordHasher()
	if err != nil {
		return err
	}
	hash, err := hasher.Hash(password)
	password = ""
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, hash+"\n")
	return err
}

func writeExclusive(path string, value []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("创建文件 %s: %w", path, err)
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入文件 %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭文件 %s: %w", path, err)
	}
	return nil
}

func writeUsage(output io.Writer) {
	_, _ = io.WriteString(output, `EnvVault 本地认证离线工具

用法:
  env-vault-auth keygen [--out-dir DIR] [--bits 3072]
  env-vault-auth hash-password [--password-env ENV_NAME]

安全约束:
  私钥只写入权限受限文件且不会覆盖已有文件
  密码只从环境变量读取，不接受命令行明文参数
`)
}
