// docker_permissions.go — Gestión de permisos Docker (Beta 8.1)
//
// Permite definir qué usuarios pueden ver/usar/instalar containers y apps
// Docker. La info se persiste en /var/lib/nimos/docker.json (campo
// "permissions" y "app_permissions").
//
// Endpoints:
//   GET  /api/docker/permissions               · lista usuarios autorizados
//   POST /api/docker/permissions               · setea lista de autorizados
//   GET  /api/docker/app-permissions           · lista permisos por app
//   POST /api/docker/app-permissions/<id>      · actualiza permisos de una app
//   GET  /api/docker/app-access/<id>           · puede el user actual usar la app?
//   GET  /api/docker/app-folders/<id>          · folders mounted por una app
//   GET  /api/permissions/matrix               · matriz user×app para admin UI
//
// Nota: hasDockerPermission() vive en docker_helpers.go porque la llaman
// también containers, stacks, install, status y pull (auth check estándar).

package main

import (
	"net/http"
	"os"
	"path/filepath"
)

func dockerPermissionsGet(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	conf := getDockerConfigGo()
	usersRaw, _ := dbUsersListRaw()
	perms, _ := conf["permissions"].([]interface{})

	var userList []map[string]interface{}
	for _, u := range usersRaw {
		hasAccess := u.Role == "admin"
		if !hasAccess {
			for _, p := range perms {
				if ps, _ := p.(string); ps == u.Username {
					hasAccess = true
					break
				}
			}
		}
		userList = append(userList, map[string]interface{}{
			"username": u.Username, "role": u.Role, "hasAccess": hasAccess,
		})
	}
	jsonOk(w, map[string]interface{}{"users": userList, "permissions": perms})
}

func dockerPermissionsSet(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	permsRaw, ok := body["permissions"].([]interface{})
	if !ok {
		jsonError(w, 400, "Invalid permissions format")
		return
	}
	conf := getDockerConfigGo()
	conf["permissions"] = permsRaw
	saveDockerConfigGo(conf)
	jsonOk(w, map[string]interface{}{"ok": true, "permissions": permsRaw})
}

func dockerAppPermissions(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	conf := getDockerConfigGo()
	usersRaw2, _ := dbUsersListRaw()
	sharesRaw, _ := dbSharesListRaw()

	var installedApps []map[string]interface{}
	containers := getRealContainersGo()
	for _, c := range containers {
		installedApps = append(installedApps, map[string]interface{}{"id": c["name"], "name": c["name"], "type": "container", "image": c["image"]})
	}

	// Check stacks
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		if dp, err := getDockerPath(); err == nil {
			dockerPath = dp
		}
	}
	stacksPath := filepath.Join(dockerPath, "stacks")
	if entries, err := os.ReadDir(stacksPath); err == nil {
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(stacksPath, e.Name(), "docker-compose.yml")); err == nil {
				installedApps = append(installedApps, map[string]interface{}{"id": e.Name(), "name": e.Name(), "type": "stack"})
			}
		}
	}

	var userList []map[string]interface{}
	for _, u := range usersRaw2 {
		userList = append(userList, map[string]interface{}{"username": u.Username, "role": u.Role})
	}

	var shareList []map[string]interface{}
	for _, s := range sharesRaw {
		shareList = append(shareList, map[string]interface{}{"name": s.Name, "displayName": s.DisplayName, "permissions": s.Permissions})
	}

	jsonOk(w, map[string]interface{}{
		"users":             userList,
		"apps":              installedApps,
		"shares":            shareList,
		"appPermissions":    conf["appPermissions"],
		"dockerPermissions": conf["permissions"],
	})
}

func dockerAppPermUpdate(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	allowedUsers, ok := body["users"].([]interface{})
	if !ok {
		jsonError(w, 400, "Invalid format")
		return
	}
	conf := getDockerConfigGo()
	appPerms, _ := conf["appPermissions"].(map[string]interface{})
	if appPerms == nil {
		appPerms = map[string]interface{}{}
	}
	appPerms[appId] = allowedUsers
	conf["appPermissions"] = appPerms
	saveDockerConfigGo(conf)
	jsonOk(w, map[string]interface{}{"ok": true, "appId": appId, "users": allowedUsers})
}

func dockerAppAccess(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if session.Role == "admin" {
		jsonOk(w, map[string]interface{}{"hasAccess": true, "appId": appId})
		return
	}
	conf := getDockerConfigGo()
	appPerms, _ := conf["appPermissions"].(map[string]interface{})
	users, _ := appPerms[appId].([]interface{})
	username := session.Username
	hasAccess := false
	for _, u := range users {
		if us, _ := u.(string); us == username {
			hasAccess = true
			break
		}
	}
	jsonOk(w, map[string]interface{}{"hasAccess": hasAccess, "appId": appId})
}

func dockerAppFolders(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	sharesRaw, _ := dbSharesListRaw()
	var folders []map[string]interface{}
	for _, s := range sharesRaw {
		for _, ap := range s.AppPermissions {
			if ap.AppId == appId {
				folders = append(folders, map[string]interface{}{"name": s.Name, "displayName": s.DisplayName, "path": s.Path})
				break
			}
		}
	}
	if folders == nil {
		folders = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"appId": appId, "folders": folders})
}
func permissionsMatrix(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	usersRaw3, _ := dbUsersListRaw()
	sharesRaw, _ := dbSharesListRaw()
	conf := getDockerConfigGo()
	perms, _ := conf["permissions"].([]interface{})

	var userList []map[string]interface{}
	for _, u := range usersRaw3 {
		hasDock := u.Role == "admin"
		for _, p := range perms {
			if ps, _ := p.(string); ps == u.Username {
				hasDock = true
			}
		}
		userList = append(userList, map[string]interface{}{"username": u.Username, "role": u.Role, "dockerAccess": hasDock})
	}

	var shareList []map[string]interface{}
	for _, s := range sharesRaw {
		appPerms := make([]map[string]interface{}, 0, len(s.AppPermissions))
		for _, ap := range s.AppPermissions {
			appPerms = append(appPerms, map[string]interface{}{"appId": ap.AppId, "uid": ap.Uid, "permission": ap.Permission})
		}
		shareList = append(shareList, map[string]interface{}{
			"name": s.Name, "displayName": s.DisplayName,
			"userPermissions": s.Permissions, "appPermissions": appPerms,
		})
	}

	jsonOk(w, map[string]interface{}{"users": userList, "shares": shareList, "dockerAdmins": perms})
}

// ═══════════════════════════════════
// Firewall
// ═══════════════════════════════════
