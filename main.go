// proxy is the standalone proxy-server binary carried by the proxy module —
// the same `proxy` command the aggregated auth-cli mounts, reading the same
// gosdk layered settings under the agentSDK namespace.
package main

import (
	"fmt"
	"os"

	"github.com/bizshuk/proxy/cmd"
)

func main() {
	root := cmd.NewCommand()
	// main 負責印錯誤與設定 exit code,cobra 不要再印一次。
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
