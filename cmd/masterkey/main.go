// Command masterkey 提供 EnvVault 主密钥分片的离线生成工具
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"env-vault/internal/masterkey"
)

const defaultMasterKeyEnv = "ENV_VAULT_MASTER_KEY"

type lookupEnvFunc func(string) (string, bool)

type shareOutput struct {
	TotalShares    int      `json:"totalShares"`
	RequiredShares int      `json:"requiredShares"`
	Shares         []string `json:"shares"`
}

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
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "split":
		return runSplit(args[1:], stdout, stderr, lookupEnv)
	default:
		writeUsage(stderr)
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func runGenerate(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "text", "输出格式：text 或 json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("generate 不接受位置参数")
	}

	tokens, err := masterkey.GenerateShareTokens()
	if err != nil {
		return err
	}
	return writeShareOutput(stdout, *format, tokens)
}

func runSplit(args []string, stdout, stderr io.Writer, lookupEnv lookupEnvFunc) error {
	flags := flag.NewFlagSet("split", flag.ContinueOnError)
	flags.SetOutput(stderr)
	keyEnv := flags.String("key-env", defaultMasterKeyEnv, "保存 Base64 主密钥的环境变量名")
	format := flags.String("format", "text", "输出格式：text 或 json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("split 不接受位置参数，主密钥只能通过环境变量传入")
	}

	envName := strings.TrimSpace(*keyEnv)
	if envName == "" || strings.Contains(envName, "=") {
		return errors.New("key-env 必须是有效的环境变量名")
	}
	keyBase64, exists := lookupEnv(envName)
	if !exists || strings.TrimSpace(keyBase64) == "" {
		return fmt.Errorf("环境变量 %s 未设置或为空", envName)
	}

	tokens, err := masterkey.SplitShareTokens(keyBase64)
	if err != nil {
		return fmt.Errorf("环境变量 %s 中的主密钥无效: %w", envName, err)
	}
	return writeShareOutput(stdout, *format, tokens)
}

func writeShareOutput(output io.Writer, format string, tokens []string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text":
		if _, err := io.WriteString(output, fmt.Sprintf(
			"EnvVault 主密钥分片（共 %d 份，恢复需要任意 %d 份）\n",
			masterkey.TotalShares,
			masterkey.RequiredShares,
		)); err != nil {
			return err
		}
		for i, token := range tokens {
			if _, err := io.WriteString(output, fmt.Sprintf("%d: %s\n", i+1, token)); err != nil {
				return err
			}
		}
		return nil
	case "json":
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(shareOutput{
			TotalShares:    masterkey.TotalShares,
			RequiredShares: masterkey.RequiredShares,
			Shares:         tokens,
		})
	default:
		return fmt.Errorf("不支持的输出格式 %q，只能使用 text 或 json", format)
	}
}

func writeUsage(output io.Writer) {
	_, _ = io.WriteString(output, `EnvVault 主密钥分片离线工具

用法:
  env-vault-masterkey generate [--format text|json]
  env-vault-masterkey split [--key-env ENV_NAME] [--format text|json]

命令:
  generate  生成新的 AES-256 主密钥并输出 3-of-5 分片
  split     从环境变量读取已有 Base64 主密钥并输出 3-of-5 分片

安全约束:
  工具不会输出完整主密钥，也不接受命令行明文主密钥参数
`)
}
