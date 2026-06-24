// socket_api.go — API privilegiada del daemon vía socket Unix.
//
// El daemon corre como root y escucha SOLO en /run/nimos-daemon.sock con un
// catálogo CERRADO de operaciones (share.*, user.*, system.reconcile). Es la
// única superficie del sistema que toca useradd/userdel/smbpasswd y la gestión
// de grupos+ACLs de shares. Nada más entra: op desconocida → rechazo logueado.
//
// Protocolo: JSON request/response, una operación por conexión, límite 64 KiB.
// (Extraído de main.go · refactor 11/06/2026.)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	socketPath = "/run/nimos-daemon.sock"
	maxReqSize = 65536
)

// ═══════════════════════════════════
// Share helpers
// ═══════════════════════════════════

func groupName(shareName string) string {
	return "nimos-share-" + shareName
}

// Share represents a share in shares.json
type Share struct {
	Name           string                   `json:"name"`
	Path           string                   `json:"path"`
	Permissions    map[string]string        `json:"permissions"`
	AppPermissions []map[string]interface{} `json:"appPermissions"`
}

// User represents a user in users.json
type User struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func readShares() ([]Share, error) {
	data, err := os.ReadFile(sharesFile)
	if err != nil {
		return nil, err
	}
	var shares []Share
	if err := json.Unmarshal(data, &shares); err != nil {
		return nil, err
	}
	return shares, nil
}

func readUsers() ([]User, error) {
	data, err := os.ReadFile(usersFile)
	if err != nil {
		return nil, err
	}
	var users []User
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func getSharePath(shareName string) (string, error) {
	if err := checkShareName(shareName); err != nil {
		return "", err
	}
	shares, err := readShares()
	if err != nil {
		return "", fmt.Errorf("cannot read shares config: %v", err)
	}
	for _, s := range shares {
		if s.Name == shareName {
			if s.Path == "" {
				return "", fmt.Errorf("share %q has no path", shareName)
			}
			if _, err := os.Stat(s.Path); os.IsNotExist(err) {
				return "", fmt.Errorf("share path does not exist: %s", s.Path)
			}
			return s.Path, nil
		}
	}
	return "", fmt.Errorf("share %q not found in config", shareName)
}

// ═══════════════════════════════════
// Request / Response types
// ═══════════════════════════════════

type Request struct {
	Op         string      `json:"op"`
	ShareName  string      `json:"shareName,omitempty"`
	RelPath    string      `json:"relPath,omitempty"`
	PoolPath   string      `json:"poolPath,omitempty"`
	Username   string      `json:"username,omitempty"`
	Password   string      `json:"password,omitempty"`
	AppId      string      `json:"appId,omitempty"`
	Uid        interface{} `json:"uid,omitempty"`
	Permission string      `json:"permission,omitempty"`
	QuotaBytes int64       `json:"quotaBytes,omitempty"`
}

type Response struct {
	Ok      bool        `json:"ok"`
	Error   string      `json:"error,omitempty"`
	Path    string      `json:"path,omitempty"`
	Existed bool        `json:"existed,omitempty"`
	Fixed   int         `json:"fixed,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ═══════════════════════════════════
// Operations catalog
// ═══════════════════════════════════

func handleOp(req Request) Response {
	switch req.Op {

	// ─── Share operations ───

	case "share.create":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkPoolPath(req.PoolPath); err != nil {
			return Response{Error: err.Error()}
		}

		sharePath := filepath.Join(req.PoolPath, "shares", req.ShareName)
		group := groupName(req.ShareName)

		runSafe("groupadd", "-f", group)

		if err := os.MkdirAll(sharePath, 0770); err != nil {
			return Response{Error: fmt.Sprintf("cannot create directory: %v", err)}
		}

		runSafe("chown", "root:"+group, sharePath)
		runSafe("chmod", "2770", sharePath)
		runSafe("setfacl", "-d", "-m", "g:"+group+":rwx", sharePath)

		// Add service user
		if _, ok := runSafe("id", serviceUser); ok {
			runSafe("usermod", "-aG", group, serviceUser)
		}

		// Add admin users
		if users, err := readUsers(); err == nil {
			for _, u := range users {
				if u.Role == "admin" && validUsername.MatchString(u.Username) {
					runSafe("usermod", "-aG", group, u.Username)
				}
			}
		}

		logMsg("share.create: %s at %s (group: %s)", req.ShareName, sharePath, group)
		return Response{Ok: true, Path: sharePath}

	case "share.delete":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		group := groupName(req.ShareName)
		runSafe("groupdel", group)
		logMsg("share.delete: %s (group removed, files preserved)", req.ShareName)
		return Response{Ok: true}

	case "share.add_user_rw":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		group := groupName(req.ShareName)
		if _, ok := runSafe("getent", "group", group); !ok {
			return Response{Error: fmt.Sprintf("group %s does not exist", group)}
		}
		runSafe("usermod", "-aG", group, req.Username)
		if sharePath, err := getSharePath(req.ShareName); err == nil {
			runSafe("setfacl", "-x", "u:"+req.Username, sharePath)
			runSafe("setfacl", "-d", "-x", "u:"+req.Username, sharePath)
		}
		logMsg("share.add_user_rw: %s → %s", req.Username, req.ShareName)
		return Response{Ok: true}

	case "share.add_user_ro":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		group := groupName(req.ShareName)
		runSafe("gpasswd", "-d", req.Username, group)
		runSafe("setfacl", "-m", "u:"+req.Username+":r-x", sharePath)
		runSafe("setfacl", "-d", "-m", "u:"+req.Username+":r-x", sharePath)
		logMsg("share.add_user_ro: %s → %s", req.Username, req.ShareName)
		return Response{Ok: true}

	case "share.remove_user":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		group := groupName(req.ShareName)
		runSafe("gpasswd", "-d", req.Username, group)
		runSafe("setfacl", "-x", "u:"+req.Username, sharePath)
		runSafe("setfacl", "-d", "-x", "u:"+req.Username, sharePath)
		logMsg("share.remove_user: %s ✕ %s", req.Username, req.ShareName)
		return Response{Ok: true}

	// ─── Managed Folder operations (Fase 3) ───
	// Carpeta gestionada = subvolumen BTRFS (quota dura) + ACL POSIX puras por
	// usuario (permisos que SMB/NFS/WebDAV respetan). NO usa grupos Linux.

	case "folder.create":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		// Cinturón: garantizar quota habilitada en el pool antes de crear el
		// subvol (idempotente). Blinda el caso de un pool recién montado que
		// aún no pasó por el barrido de arranque.
		poolMount := poolMountFromSharePath(sharePath)
		if poolMount != "" {
			if qerr := ensureBtrfsQuotaEnabled(poolMount); qerr != nil {
				logMsg("folder.create: WARNING no se pudo habilitar quota en %s: %v", poolMount, qerr)
			}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		if _, statErr := os.Stat(folderPath); statErr == nil {
			return Response{Error: "folder already exists"}
		}
		// Crear como SUBVOLUMEN (no mkdir) para que pueda llevar qgroup.
		if out, ok := runSafe("btrfs", "subvolume", "create", folderPath); !ok {
			return Response{Error: fmt.Sprintf("btrfs subvolume create failed: %s", out)}
		}
		runSafe("chmod", "0770", folderPath)
		// Quota dura si se especificó.
		if req.QuotaBytes > 0 {
			if out, ok := runSafe("btrfs", "qgroup", "limit", fmt.Sprintf("%d", req.QuotaBytes), folderPath); !ok {
				logMsg("folder.create: WARNING qgroup limit falló en %s: %s", folderPath, out)
			}
		}
		logMsg("folder.create: %s en share %s (quota=%d)", req.RelPath, req.ShareName, req.QuotaBytes)
		return Response{Ok: true, Path: folderPath}

	case "folder.set_quota":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		if _, statErr := os.Stat(folderPath); statErr != nil {
			return Response{Error: "folder not found"}
		}
		var out string
		var ok bool
		if req.QuotaBytes > 0 {
			out, ok = runSafe("btrfs", "qgroup", "limit", fmt.Sprintf("%d", req.QuotaBytes), folderPath)
		} else {
			out, ok = runSafe("btrfs", "qgroup", "limit", "none", folderPath)
		}
		if !ok {
			return Response{Error: fmt.Sprintf("qgroup limit failed: %s", out)}
		}
		logMsg("folder.set_quota: %s en %s → %d", req.RelPath, req.ShareName, req.QuotaBytes)
		return Response{Ok: true}

	case "folder.set_perm_rw":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		// ACL POSIX pura por usuario: acceso + default (hereda en lo nuevo).
		runSafe("setfacl", "-m", "u:"+req.Username+":rwx", folderPath)
		runSafe("setfacl", "-d", "-m", "u:"+req.Username+":rwx", folderPath)
		logMsg("folder.set_perm_rw: %s → %s/%s", req.Username, req.ShareName, req.RelPath)
		return Response{Ok: true}

	case "folder.set_perm_ro":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		runSafe("setfacl", "-m", "u:"+req.Username+":r-x", folderPath)
		runSafe("setfacl", "-d", "-m", "u:"+req.Username+":r-x", folderPath)
		logMsg("folder.set_perm_ro: %s → %s/%s", req.Username, req.ShareName, req.RelPath)
		return Response{Ok: true}

	case "folder.remove_perm":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		runSafe("setfacl", "-x", "u:"+req.Username, folderPath)
		runSafe("setfacl", "-d", "-x", "u:"+req.Username, folderPath)
		logMsg("folder.remove_perm: %s ✕ %s/%s", req.Username, req.ShareName, req.RelPath)
		return Response{Ok: true}

	case "folder.delete":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkFolderRelPath(req.RelPath); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getManagedSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		folderPath := filepath.Join(sharePath, req.RelPath)
		if _, statErr := os.Stat(folderPath); statErr != nil {
			return Response{Error: "folder not found"}
		}
		// v1: NO se borra si tiene contenido. Sin borrado recursivo.
		empty, eerr := dirIsEmpty(folderPath)
		if eerr != nil {
			return Response{Error: fmt.Sprintf("cannot check folder: %v", eerr)}
		}
		if !empty {
			return Response{Error: "folder_not_empty"}
		}
		if out, ok := runSafe("btrfs", "subvolume", "delete", folderPath); !ok {
			return Response{Error: fmt.Sprintf("btrfs subvolume delete failed: %s", out)}
		}
		logMsg("folder.delete: %s en share %s", req.RelPath, req.ShareName)
		return Response{Ok: true}

	case "share.add_app":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		uid, err := checkUid(req.Uid)
		if err != nil {
			return Response{Error: err.Error()}
		}
		if err := checkPermission(req.Permission); err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		acl := "r-x"
		if req.Permission == "rw" {
			acl = "rwx"
		}
		runSafe("setfacl", "-m", fmt.Sprintf("u:%d:%s", uid, acl), sharePath)
		runSafe("setfacl", "-d", "-m", fmt.Sprintf("u:%d:%s", uid, acl), sharePath)
		logMsg("share.add_app: %s (uid:%d) → %s (%s)", req.AppId, uid, req.ShareName, req.Permission)
		return Response{Ok: true}

	case "share.remove_app":
		if err := checkShareName(req.ShareName); err != nil {
			return Response{Error: err.Error()}
		}
		uid, err := checkUid(req.Uid)
		if err != nil {
			return Response{Error: err.Error()}
		}
		sharePath, err := getSharePath(req.ShareName)
		if err != nil {
			return Response{Error: err.Error()}
		}
		runSafe("setfacl", "-x", fmt.Sprintf("u:%d", uid), sharePath)
		runSafe("setfacl", "-d", "-x", fmt.Sprintf("u:%d", uid), sharePath)
		logMsg("share.remove_app: %s (uid:%d) ✕ %s", req.AppId, uid, req.ShareName)
		return Response{Ok: true}

	// ─── User operations ───

	case "user.create":
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		if _, ok := runSafe("id", req.Username); ok {
			logMsg("user.create: %s already exists — skipping", req.Username)
			return Response{Ok: true, Existed: true}
		}
		runSafe("useradd", "-M", "-s", "/usr/sbin/nologin", req.Username)
		if _, ok := runSafe("id", req.Username); !ok {
			return Response{Error: fmt.Sprintf("failed to create Linux user: %s", req.Username)}
		}
		logMsg("user.create: %s", req.Username)
		return Response{Ok: true}

	case "user.delete":
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		shell, _ := runSafe("sh", "-c", fmt.Sprintf(`getent passwd "%s" 2>/dev/null | cut -d: -f7`, req.Username))
		if !strings.Contains(shell, "nologin") {
			return Response{Error: fmt.Sprintf("refusing to delete %s: not a NimOS-managed user", req.Username)}
		}
		runSafe("smbpasswd", "-x", req.Username)
		runSafe("userdel", req.Username)
		logMsg("user.delete: %s", req.Username)
		return Response{Ok: true}

	case "user.set_smb_password":
		if err := checkUsername(req.Username); err != nil {
			return Response{Error: err.Error()}
		}
		if req.Password == "" {
			return Response{Error: "password required"}
		}
		// Ensure user exists
		if _, ok := runSafe("id", req.Username); !ok {
			runSafe("useradd", "-M", "-s", "/usr/sbin/nologin", req.Username)
		}
		// Set samba password via stdin
		cmd := exec.Command("smbpasswd", "-s", "-a", req.Username)
		cmd.Stdin = strings.NewReader(req.Password + "\n" + req.Password + "\n")
		if err := cmd.Run(); err != nil {
			return Response{Error: fmt.Sprintf("failed to set Samba password for %s", req.Username)}
		}
		logMsg("user.set_smb_password: %s", req.Username)
		return Response{Ok: true}

	// ─── System operations ───

	case "system.reconcile":
		return reconcile()

	// ─── NOTE: Database operations (db.*) removed from privileged daemon ───
	// HTTP handlers call db functions directly (dbUsersList, dbSharesGet, etc.)
	// The daemon socket only handles privileged OS operations (users, shares, ACLs)

	default:
		logMsg("rejected unknown op: %s", req.Op)
		return Response{Error: fmt.Sprintf("unknown operation: %s", req.Op)}
	}
}

// ═══════════════════════════════════
// Reconciliation
// ═══════════════════════════════════

func reconcile() Response {
	logMsg("system.reconcile: starting...")
	fixed := 0

	shares, err := dbSharesListRaw()
	if err != nil {
		logMsg("  reconcile error: %v", err)
		return Response{Error: err.Error(), Fixed: fixed}
	}

	for _, share := range shares {
		if share.Name == "" || share.Path == "" {
			continue
		}
		group := groupName(share.Name)

		// 1. Ensure group exists
		if _, ok := runSafe("getent", "group", group); !ok {
			runSafe("groupadd", "-f", group)
			logMsg("  reconcile: created group %s", group)
			fixed++
		}

		// 2. Ensure directory permissions (skip if quota is near full to avoid blocking)
		if _, err := os.Stat(share.Path); err == nil {
			avail := getAvailableBytes(share.Path)
			if avail < 1024*1024 { // less than 1MB free — skip permissions
				logMsg("  reconcile: skipping permissions for %s (disk full, %d bytes free)", share.Name, avail)
			} else {
				runSafe("chown", "root:"+group, share.Path)
				runSafe("chmod", "2770", share.Path)
				runSafe("setfacl", "-d", "-m", "g:"+group+":rwx", share.Path)
			}
		}

		// 3. Ensure user permissions match DB
		for username, perm := range share.Permissions {
			if !validUsername.MatchString(username) || systemUsers[username] {
				continue
			}
			if perm == "rw" {
				groups, ok := runSafe("id", "-nG", username)
				if ok && !containsWord(groups, group) {
					runSafe("usermod", "-aG", group, username)
					logMsg("  reconcile: added %s to %s (rw)", username, group)
					fixed++
				}
			} else if perm == "ro" {
				runSafe("gpasswd", "-d", username, group)
				runSafe("setfacl", "-m", "u:"+username+":r-x", share.Path)
				runSafe("setfacl", "-d", "-m", "u:"+username+":r-x", share.Path)
			}
		}

		// 4. Ensure app permissions
		for _, app := range share.AppPermissions {
			acl := "r-x"
			if app.Permission == "rw" {
				acl = "rwx"
			}
			runSafe("setfacl", "-m", fmt.Sprintf("u:%d:%s", app.Uid, acl), share.Path)
			runSafe("setfacl", "-d", "-m", fmt.Sprintf("u:%d:%s", app.Uid, acl), share.Path)
		}

		// 5. Service user must be in ALL share groups
		if _, ok := runSafe("id", serviceUser); ok {
			groups, _ := runSafe("id", "-nG", serviceUser)
			if !containsWord(groups, group) {
				runSafe("usermod", "-aG", group, serviceUser)
				logMsg("  reconcile: added service user %s to %s", serviceUser, group)
				fixed++
			}
		}

		// 6. Admin users always get rw on ALL shares
		if adminUsers, err := dbUsersListRaw(); err == nil {
			for _, u := range adminUsers {
				if u.Role == "admin" && validUsername.MatchString(u.Username) {
					groups, _ := runSafe("id", "-nG", u.Username)
					if !containsWord(groups, group) {
						runSafe("usermod", "-aG", group, u.Username)
						logMsg("  reconcile: added admin %s to %s", u.Username, group)
						fixed++
					}
				}
			}
		}
	}

	// Cleanup expired sessions
	cleaned := dbSessionCleanup()
	if cleaned > 0 {
		logMsg("  reconcile: cleaned %d expired sessions", cleaned)
	}

	logMsg("system.reconcile: done (%d fixes applied)", fixed)
	return Response{Ok: true, Fixed: fixed}
}

func containsWord(s, word string) bool {
	for _, w := range strings.Fields(s) {
		if w == word {
			return true
		}
	}
	return false
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read request with size limit
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if len(data) > maxReqSize {
			writeResponse(conn, Response{Error: "request too large"})
			return
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			logMsg("Read error: %v", err)
			return
		}
	}

	// Parse request
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		writeResponse(conn, Response{Error: "invalid JSON"})
		return
	}

	if req.Op == "" {
		writeResponse(conn, Response{Error: "missing op"})
		return
	}

	// Log (mask password)
	logData := string(data)
	if req.Password != "" {
		logData = strings.Replace(logData, req.Password, "***", -1)
	}
	logMsg("→ %s %s", req.Op, logData)

	// Execute
	resp := handleOp(req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
}

// ═══════════════════════════════════
// Socket server
// ═══════════════════════════════════

// runSocketServer crea el socket Unix, instala el manejador de apagado
// graceful (SIGTERM/SIGINT) y bloquea en el accept loop hasta el shutdown.
// Comportamiento idéntico al main() previo al refactor: solo reubicado.
func runSocketServer() {
	// Clean up stale socket
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		logMsg("Fatal: cannot listen on %s: %v", socketPath, err)
		os.Exit(1)
	}
	defer listener.Close()

	// Set socket permissions: service user can connect
	os.Chmod(socketPath, 0660)
	// Change group to service user's group so the web server can connect
	runSafe("chgrp", serviceUser, socketPath)

	logMsg("Listening on %s", socketPath)

	// Run reconciliation on startup
	reconcile()

	// Graceful shutdown
	installShutdownHandler(listener)

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Check if we're shutting down
			if strings.Contains(err.Error(), "use of closed") {
				break
			}
			logMsg("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}
