package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "0.2.0"

type Machine struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	GPUModel     string            `json:"gpuModel"`
	Status       string            `json:"status"`
	AgentVersion string            `json:"agentVersion"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	LastSeenAt   *time.Time        `json:"lastSeenAt"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type Forward struct {
	ID         string `json:"id"`
	MachineID  string `json:"machineId"`
	Name       string `json:"name"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	PublicPort int    `json:"publicPort"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
}

type AvailablePorts struct {
	Ports []int `json:"ports"`
	Total int   `json:"total"`
}

type MachineList struct {
	Items []Machine `json:"items"`
}

type ForwardList struct {
	Items []Forward `json:"items"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "register":
		cmdRegister()
	case "forward":
		cmdForward()
	case "status":
		cmdStatus()
	case "daemon":
		cmdDaemon()
	case "list":
		cmdList()
	case "delete":
		cmdDelete()
	case "ssh":
		cmdSSH()
	case "uninstall":
		cmdUninstall()
	case "version":
		fmt.Println("sshr version", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`sshr - SSH Relay CLI client

Commands:
  sshr register     Register this machine with the relay server
  sshr forward      Browse available ports and create a forward
  sshr status       Show machine status and active forwards
  sshr daemon       Start heartbeat + reverse tunnel daemon
  sshr list         List all forwards for this machine
  sshr delete       Delete a forward by port number
  sshr ssh <name>   SSH into a registered machine by name
  sshr uninstall     Remove systemd service and local config
  sshr version      Show version

Config file: ~/.config/sshr/machine.json
Override env: SSHR_SERVER, SSHR_MACHINE_ID`)
}

func serverURL() string {
	if s := os.Getenv("SSHR_SERVER"); s != "" {
		return s
	}
	c, err := loadConfig()
	if err != nil {
		return ""
	}
	return c.Server
}

func relayHost() string {
	u, err := url.Parse(serverURL())
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func configDir() string {
	d := os.Getenv("HOME")
	if d == "" {
		d = "/root"
	}
	return filepath.Join(d, ".config", "sshr")
}

func configFile() string {
	return filepath.Join(configDir(), "machine.json")
}

type configData struct {
	MachineID string `json:"machineId"`
	Name      string `json:"name"`
	Server    string `json:"server"`
}

func loadConfig() (configData, error) {
	data, err := os.ReadFile(configFile())
	if err != nil {
		return configData{}, err
	}
	var c configData
	if err := json.Unmarshal(data, &c); err != nil {
		return configData{}, err
	}
	return c, nil
}

func saveConfig(c configData) error {
	os.MkdirAll(configDir(), 0700)
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configFile(), data, 0600)
}

func machineID() string {
	if id := os.Getenv("SSHR_MACHINE_ID"); id != "" {
		return id
	}
	c, err := loadConfig()
	if err != nil {
		return ""
	}
	return c.MachineID
}

// ─── register ───

func cmdRegister() {
	server := serverURL()
	fmt.Printf("Registering with %s\n", server)

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Machine name (leave blank for auto-generate): ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)

	gpuModel := ""
	if name == "" {
		gpuModel = detectGPU()
		if gpuModel == "" {
			gpuModel = "unknown"
		}
		randChars := randomHex(4)
		name = sanitizeGPUName(gpuModel) + "-" + randChars
		fmt.Printf("Auto-detected GPU: %s\n", gpuModel)
		fmt.Printf("Generated name: %s\n", name)
	}

	hostname, _ := os.Hostname()
	payload := map[string]any{
		"name":         name,
		"gpuModel":     gpuModel,
		"agentVersion": version,
		"metadata": map[string]string{
			"hostname": hostname,
			"os":       "linux",
		},
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(server+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		log.Fatalf("name conflict: %s\nTry again with a different name.", errResp["error"])
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Fatalf("register failed (%s): %s", resp.Status, string(bodyBytes))
	}

	var m Machine
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		log.Fatalf("decode response: %v", err)
	}

	if err := saveConfig(configData{MachineID: m.ID, Name: m.Name, Server: server}); err != nil {
		log.Fatalf("save config: %v", err)
	}

	fmt.Printf("Registered successfully!\n")
	fmt.Printf("  Machine ID: %s\n", m.ID)
	fmt.Printf("  Name:       %s\n", m.Name)
	fmt.Printf("  GPU:        %s\n", m.GPUModel)
	fmt.Printf("  Config:     %s\n", configFile())
}

// ─── forward ───

func cmdForward() {
	id := machineID()
	if id == "" {
		log.Fatal("not registered. Run 'sshr register' first.")
	}
	server := serverURL()

	resp, err := http.Get(server + "/api/ports/available")
	if err != nil {
		log.Fatalf("fetch ports: %v", err)
	}
	defer resp.Body.Close()

	var avail AvailablePorts
	if err := json.NewDecoder(resp.Body).Decode(&avail); err != nil {
		log.Fatalf("decode ports: %v", err)
	}

	if len(avail.Ports) == 0 {
		log.Fatal("no available ports in the configured range.")
	}

	fmt.Println("Available ports:")
	for _, p := range avail.Ports {
		fmt.Printf("  %d\n", p)
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Select port [%d]: ", avail.Ports[0])
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		portStr = fmt.Sprintf("%d", avail.Ports[0])
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	fmt.Print("Target host [127.0.0.1]: ")
	targetHost, _ := reader.ReadString('\n')
	targetHost = strings.TrimSpace(targetHost)
	if targetHost == "" {
		targetHost = "127.0.0.1"
	}

	fmt.Print("Target port [22]: ")
	tpStr, _ := reader.ReadString('\n')
	tpStr = strings.TrimSpace(tpStr)
	if tpStr == "" {
		tpStr = "22"
	}
	var targetPort int
	fmt.Sscanf(tpStr, "%d", &targetPort)

	fmt.Print("Forward name (optional): ")
	fwdName, _ := reader.ReadString('\n')
	fwdName = strings.TrimSpace(fwdName)
	if fwdName == "" {
		fwdName = fmt.Sprintf("ssh-%d", port)
	}

	payload := map[string]any{
		"machineId":  id,
		"name":       fwdName,
		"targetHost": targetHost,
		"targetPort": targetPort,
		"publicPort": port,
	}
	body, _ := json.Marshal(payload)
	resp2, err := http.Post(server+"/api/forwards", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("create forward: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusCreated {
		respBytes, _ := io.ReadAll(resp2.Body)
		log.Fatalf("create forward failed (%s): %s", resp.Status, string(respBytes))
	}

	var f Forward
	json.NewDecoder(resp2.Body).Decode(&f)
	host := relayHost()
	fmt.Printf("Forward created: %d -> %s:%d\n", f.PublicPort, f.TargetHost, f.TargetPort)
	fmt.Printf("SSH access: ssh -p %d user@%s\n", f.PublicPort, host)
	fmt.Println("(make sure sshr daemon is running to maintain the reverse tunnel)")
}

// ─── status ───

func cmdStatus() {
	id := machineID()
	if id == "" {
		log.Fatal("not registered. Run 'sshr register' first.")
	}
	server := serverURL()

	resp, err := http.Get(server + "/api/machines/" + id)
	if err != nil {
		log.Fatalf("fetch machine: %v", err)
	}
	defer resp.Body.Close()

	var m Machine
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		log.Fatalf("decode: %v", err)
	}

	statusIcon := "●"
	if m.Status != "online" {
		statusIcon = "○"
	}

	fmt.Printf("Machine: %s %s\n", m.Name, statusIcon+m.Status)
	fmt.Printf("  ID:    %s\n", m.ID)
	if m.GPUModel != "" {
		fmt.Printf("  GPU:   %s\n", m.GPUModel)
	}
	lastSeen := "never"
	if m.LastSeenAt != nil {
		lastSeen = time.Since(*m.LastSeenAt).Round(time.Second).String() + " ago"
	}
	fmt.Printf("  Last seen: %s\n", lastSeen)

	fwds, _ := fetchForwards(server, id)
	if len(fwds) == 0 {
		fmt.Println("\nNo active forwards.")
	} else {
		host := relayHost()
		fmt.Println("\nActive forwards:")
		for _, f := range fwds {
			fmt.Printf("  %d -> %s:%d  (%s)\n", f.PublicPort, f.TargetHost, f.TargetPort, f.Name)
			fmt.Printf("    ssh -p %d user@%s\n", f.PublicPort, host)
		}
	}
}

// ─── daemon (heartbeat + reverse tunnels) ───

func cmdDaemon() {
	id := machineID()
	if id == "" {
		log.Fatal("not registered. Run 'sshr register' first.")
	}
	server := serverURL()
	host := relayHost()

	log.Printf("sshr daemon %s starting (machine=%s)", version, id)

	doHeartbeat := func() {
		payload := map[string]any{
			"machineId":    id,
			"agentVersion": version,
			"metadata":     map[string]string{},
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(server+"/api/heartbeat", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("heartbeat error: %v", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			log.Fatalf("machine not found on server. Re-register with 'sshr register'.")
		}
	}

	// Manage reverse tunnels
	tunnels := &tunnelManager{
		server: server,
		host:   host,
		machineID: id,
	}

	doHeartbeat()
	tunnels.sync()

	heartbeatTicker := time.NewTicker(30 * time.Second)
	syncTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()
	defer syncTicker.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-heartbeatTicker.C:
			doHeartbeat()
		case <-syncTicker.C:
			tunnels.sync()
		case <-sig:
			log.Println("daemon stopping...")
			tunnels.stopAll()
			log.Println("daemon stopped")
			return
		}
	}
}

type tunnelManager struct {
	server    string
	host      string
	machineID string
	mu        sync.Mutex
	active    map[int]context.CancelFunc
}

func (tm *tunnelManager) sync() {
	fwds, err := fetchForwards(tm.server, tm.machineID)
	if err != nil {
		return
	}

	desired := map[int]Forward{}
	for _, f := range fwds {
		desired[f.PublicPort] = f
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.active == nil {
		tm.active = map[int]context.CancelFunc{}
	}

	// Stop stale
	for port, cancel := range tm.active {
		if _, ok := desired[port]; !ok {
			cancel()
			delete(tm.active, port)
			log.Printf("tunnel closed: port %d", port)
		}
	}

	// Start new
	for port := range desired {
		if _, ok := tm.active[port]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		tm.active[port] = cancel
		go runReverseTunnel(ctx, tm.server, tm.machineID, port)
		log.Printf("tunnel opened: port %d", port)
	}
}

func (tm *tunnelManager) stopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, cancel := range tm.active {
		cancel()
	}
	tm.active = nil
}

func runReverseTunnel(ctx context.Context, server, machineID string, publicPort int) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := maintainReverse(ctx, server, machineID, publicPort); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("reverse tunnel port %d: %v -- retrying in 3s", publicPort, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func maintainReverse(ctx context.Context, server, machineID string, publicPort int) error {
	parsed, err := url.Parse(server)
	if err != nil {
		return err
	}
	host := parsed.Host
	if !strings.Contains(host, ":") {
		if parsed.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// HTTP upgrade request
	req := fmt.Sprintf("GET /api/relay/reverse HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: sshr-reverse\r\nX-Machine-Id: %s\r\nX-Public-Port: %d\r\n\r\n",
		parsed.Host, machineID, publicPort)
	if _, err := conn.Write([]byte(req)); err != nil {
		return fmt.Errorf("write upgrade: %w", err)
	}

	// Read 101 response
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(reader, 512))
		return fmt.Errorf("upgrade rejected: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// Wait for proxy commands from server
	bufConn := &bufConn{Conn: conn, reader: reader}
	handleProxyCommands(ctx, bufConn)
	return nil
}

type bufConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func handleProxyCommands(ctx context.Context, conn net.Conn) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn.SetReadDeadline(time.Now().Add(keepaliveInterval * 2))

		// Read SSHR-PROXY header
		header, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		header = strings.TrimSpace(header)

		if !strings.HasPrefix(header, "SSHR-PROXY ") {
			continue
		}

		var bodyLen int
		fmt.Sscanf(strings.TrimPrefix(header, "SSHR-PROXY "), "%d", &bodyLen)

		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}

		var cmd struct {
			TargetHost string `json:"targetHost"`
			TargetPort int    `json:"targetPort"`
		}
		if err := json.Unmarshal(body, &cmd); err != nil {
			return
		}

		go proxyToLocal(ctx, conn, cmd.TargetHost, cmd.TargetPort)
	}
}

func proxyToLocal(ctx context.Context, relay net.Conn, targetHost string, targetPort int) {
	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort), 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()

	errCh := make(chan error, 2)
	go func() { _, e := io.Copy(target, relay); errCh <- e }()
	go func() { _, e := io.Copy(relay, target); errCh <- e }()
	<-errCh
}

const keepaliveInterval = 30 * time.Second

// ─── list ───

func cmdList() {
	id := machineID()
	if id == "" {
		log.Fatal("not registered.")
	}
	server := serverURL()
	host := relayHost()

	fwds, err := fetchForwards(server, id)
	if err != nil {
		log.Fatalf("fetch: %v", err)
	}

	if len(fwds) == 0 {
		fmt.Println("No active forwards.")
		return
	}

	fmt.Println("Active forwards:")
	for _, f := range fwds {
		fmt.Printf("  %d -> %s:%d  (%s)\n", f.PublicPort, f.TargetHost, f.TargetPort, f.Name)
		fmt.Printf("    ssh -p %d user@%s\n", f.PublicPort, host)
	}
}

// ─── delete ───

func cmdDelete() {
	id := machineID()
	if id == "" {
		log.Fatal("not registered.")
	}
	server := serverURL()

	fwds, err := fetchForwards(server, id)
	if err != nil {
		log.Fatalf("fetch: %v", err)
	}

	if len(fwds) == 0 {
		fmt.Println("No active forwards.")
		return
	}

	fmt.Println("Active forwards:")
	for _, f := range fwds {
		fmt.Printf("  %d -> %s:%d  (%s)  [%s]\n", f.PublicPort, f.TargetHost, f.TargetPort, f.Name, f.ID)
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter port number to delete: ")
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)

	var fwdID string
	for _, f := range fwds {
		if fmt.Sprintf("%d", f.PublicPort) == portStr {
			fwdID = f.ID
			break
		}
	}
	if fwdID == "" {
		log.Fatalf("no forward found on port %s", portStr)
	}

	req, _ := http.NewRequest(http.MethodDelete, server+"/api/forwards/"+fwdID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("Forward on port %s deleted.\n", portStr)
}

// ─── ssh: connect to a remote machine by name ───

func cmdSSH() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: sshr ssh <machine-name>")
		fmt.Println("  Connects via SSH to a registered machine by name.")
		fmt.Println("  Extra arguments after -- are passed to ssh.")
		os.Exit(1)
	}

	name := os.Args[2]
	server := serverURL()
	host := relayHost()

	// Collect extra ssh args after --
	var sshArgs []string
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			sshArgs = os.Args[i+1:]
			break
		}
	}

	// Find the machine by name
	resp, err := http.Get(server + "/api/machines")
	if err != nil {
		log.Fatalf("query machines: %v", err)
	}
	defer resp.Body.Close()

	var machines MachineList
	if err := json.NewDecoder(resp.Body).Decode(&machines); err != nil {
		log.Fatalf("decode: %v", err)
	}

	var target Machine
	for _, m := range machines.Items {
		if strings.EqualFold(m.Name, name) {
			target = m
			break
		}
	}
	if target.ID == "" {
		log.Fatalf("machine %q not found", name)
	}

	if target.Status != "online" {
		log.Printf("Warning: machine %q is %s", name, target.Status)
	}

	// Get its forwards
	fwds, err := fetchForwards(server, target.ID)
	if err != nil {
		log.Fatalf("fetch forwards: %v", err)
	}

	if len(fwds) == 0 {
		log.Fatalf("machine %q has no active forwards", name)
	}

	// If multiple forwards, use the first one (typically port 22)
	fwd := fwds[0]
	if len(fwds) > 1 {
		fmt.Printf("Machine %q has %d forwards:\n", name, len(fwds))
		for _, f := range fwds {
			fmt.Printf("  %d -> %s:%d  (%s)\n", f.PublicPort, f.TargetHost, f.TargetPort, f.Name)
		}
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Which port to use [%d]: ", fwds[0].PublicPort)
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)
		if choice != "" {
			var chosenPort int
			fmt.Sscanf(choice, "%d", &chosenPort)
			for _, f := range fwds {
				if f.PublicPort == chosenPort {
					fwd = f
					break
				}
			}
		}
	}

	fmt.Printf("Connecting to %s via port %d...\n", name, fwd.PublicPort)

	sshCmd := []string{"ssh", "-p", fmt.Sprintf("%d", fwd.PublicPort)}
	sshCmd = append(sshCmd, sshArgs...)
	sshCmd = append(sshCmd, host)

	cmd := exec.Command(sshCmd[0], sshCmd[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Exit(cmd.ProcessState.ExitCode())
	}
}

// ─── uninstall ───

func cmdUninstall() {
	fmt.Println("This will remove sshr systemd service and configuration.")
	fmt.Print("Continue? [y/N]: ")
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("Cancelled.")
		return
	}

	// Stop and disable systemd user service
	exec.Command("systemctl", "--user", "stop", "sshrdaemon").Run()
	exec.Command("systemctl", "--user", "disable", "sshrdaemon").Run()
	os.Remove(os.ExpandEnv("$HOME/.config/systemd/user/sshrdaemon.service"))
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("systemd service removed.")

	// Remove config
	os.RemoveAll(configDir())
	fmt.Println("Configuration removed.")

	// Remove binary
	binPath, _ := exec.LookPath("sshr")
	if binPath != "" {
		fmt.Printf("To remove the binary: sudo rm %s\n", binPath)
	}
	fmt.Println("Uninstall complete.")
}

// ─── helpers ───

func fetchForwards(server, machineID string) ([]Forward, error) {
	resp, err := http.Get(server + "/api/forwards?machineId=" + machineID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var fwds ForwardList
	if err := json.NewDecoder(resp.Body).Decode(&fwds); err != nil {
		return nil, err
	}
	return fwds.Items, nil
}

func detectGPU() string {
	out, err := exec.Command("lspci", "-mm").Output()
	if err != nil {
		out2, err2 := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
		if err2 == nil {
			return strings.TrimSpace(string(out2))
		}
		return ""
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "vga") || strings.Contains(lower, "3d") || strings.Contains(lower, "display") {
			if strings.Contains(lower, "nvidia") || strings.Contains(lower, "amd") || strings.Contains(lower, "intel") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 8 {
					return strings.TrimSpace(parts[5] + " " + parts[7])
				}
			}
		}
	}
	return ""
}

func sanitizeGPUName(name string) string {
	s := strings.ReplaceAll(name, " ", "")
	s = strings.ReplaceAll(s, "NVIDIA", "NV")
	s = strings.ReplaceAll(s, "GeForce", "")
	s = strings.ReplaceAll(s, "Corporation", "")
	s = strings.ReplaceAll(s, "AdvancedMicroDevices", "AMD")
	s = strings.ReplaceAll(s, "[AMD/ATI]", "AMD")
	s = strings.ReplaceAll(s, "Radeon", "")
	s = strings.ReplaceAll(s, "[", "")
	s = strings.ReplaceAll(s, "]", "")
	s = strings.ReplaceAll(s, "/", "")
	if len(s) > 20 {
		s = s[:20]
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
