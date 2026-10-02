package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// ---------- CHAP 口令静态加密(AES-256-GCM,密钥取自 manager.json) ----------

func (a *App) chapEncrypt(plain string) (string, error) {
	key, err := a.cfg.DataKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (a *App) chapDecrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	key, err := a.cfg.DataKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文非法")
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ---------- 主机 ----------

type initiatorRow struct {
	ID        int64  `json:"id"`
	HostID    int64  `json:"host_id"`
	Transport string `json:"transport"`
	IQN       string `json:"iqn"` // iSCSI IQN 或 FC WWPN
	ChapUser  string `json:"chap_user"`
	HasChap   bool   `json:"has_chap"`
}

func (a *App) handleHostsList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `
		SELECT h.id,h.name,h.description,h.os_type,h.created_at,
		  (SELECT count(*) FROM initiators i WHERE i.host_id=h.id),
		  (SELECT count(*) FROM mappings m WHERE m.host_id=h.id),
		  COALESCE((SELECT string_agg(DISTINCT i.transport, ',') FROM initiators i WHERE i.host_id=h.id), '')
		FROM hosts h ORDER BY h.id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		OsType         string `json:"os_type"`
		InitiatorCount int64  `json:"initiator_count"`
		MappingCount   int64  `json:"mapping_count"`
		Transports     string `json:"transports"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Name, &it.Description, &it.OsType,
			newTimePtr(), &it.InitiatorCount, &it.MappingCount, &it.Transports); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, it)
	}
	writeOK(w, map[string]any{"hosts": out})
}

func (a *App) handleHostGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var h struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		OsType      string `json:"os_type"`
		CreatedAt   string `json:"created_at"`
	}
	if err := a.db.QueryRow(r.Context(), `
		SELECT id,name,description,os_type,created_at::text FROM hosts WHERE id=$1`, id).
		Scan(&h.ID, &h.Name, &h.Description, &h.OsType, &h.CreatedAt); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "主机不存在")
		return
	}
	inits, err := a.initiatorsOf(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	// 该主机可见卷(经 mapping)
	vrows, err := a.db.Query(r.Context(), `
		SELECT d.id,d.name,d.filesystem_type,d.filesystem_status,t.target_name,l.lun_id,m.rw
		FROM mappings m
		JOIN datasets d ON d.id=m.dataset_id
		JOIN targets t ON t.id=m.target_id
		JOIN luns l ON l.target_id=m.target_id AND l.dataset_id=m.dataset_id
		WHERE m.host_id=$1 ORDER BY d.name`, id)
	vols := []map[string]any{}
	if err == nil {
		for vrows.Next() {
			var did int64
			var dsName, fs, fsst, iqn string
			var lun int64
			var rw string
			if vrows.Scan(&did, &dsName, &fs, &fsst, &iqn, &lun, &rw) == nil {
				vols = append(vols, map[string]any{
					"dataset_id": did, "dataset": dsName, "filesystem_type": fs,
					"filesystem_status": fsst, "iqn": iqn, "lun_id": lun, "rw": rw,
				})
			}
		}
		vrows.Close()
	}
	writeOK(w, map[string]any{"host": h, "initiators": inits, "volumes": vols})
}

func (a *App) initiatorsOf(r *http.Request, hostID int64) ([]initiatorRow, error) {
	rows, err := a.db.Query(r.Context(), `
		SELECT id,host_id,transport,iqn,chap_user,(chap_secret_enc <> '') FROM initiators
		WHERE host_id=$1 ORDER BY id`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []initiatorRow{}
	for rows.Next() {
		var it initiatorRow
		if err := rows.Scan(&it.ID, &it.HostID, &it.Transport, &it.IQN, &it.ChapUser, &it.HasChap); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

func (a *App) handleHostCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		OsType      string `json:"os_type"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(body.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "主机名非法")
		return
	}
	var id int64
	err := a.db.QueryRow(r.Context(), `
		INSERT INTO hosts(name,description,os_type) VALUES($1,$2,$3) RETURNING id`,
		body.Name, body.Description, body.OsType).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusConflict, "CONFLICT", "主机名重复或写入失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"host_id": id})
}

func (a *App) handleHostUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var body struct {
		Description *string `json:"description"`
		OsType      *string `json:"os_type"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.Description != nil || body.OsType != nil {
		desc := ""
		if body.Description != nil {
			desc = *body.Description
		}
		osType := ""
		if body.OsType != nil {
			osType = *body.OsType
		}
		if _, err := a.db.Exec(r.Context(),
			`UPDATE hosts SET description=$2, os_type=$3 WHERE id=$1`, id, desc, osType); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
	}
	writeOK(w, map[string]any{"updated": id})
}

func (a *App) handleHostDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var mcnt int64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM mappings WHERE host_id=$1`, id).Scan(&mcnt)
	if mcnt > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "主机仍有 %d 条映射,请先解除映射", mcnt)
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM hosts WHERE id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	writeOK(w, map[string]any{"deleted": id})
}

type initAddReq struct {
	Transport  string `json:"transport"` // iscsi(默认) | fc
	IQN        string `json:"iqn"`       // iSCSI IQN 或 FC WWPN
	WWN        string `json:"wwn"`       // FC WWPN 别名
	ChapUser   string `json:"chap_user"`
	ChapSecret string `json:"chap_secret"`
}

func (a *App) handleInitiatorAdd(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var req initAddReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if req.Transport == "" {
		req.Transport = "iscsi"
	}
	if req.Transport == "fc" {
		if req.WWN != "" {
			req.IQN = req.WWN
		}
		wwn, ok := normalizeWWN(req.IQN)
		if !ok {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "WWPN 非法(示例: 0x10000000c9a1b2c3)")
			return
		}
		req.IQN = wwn
		if req.ChapUser != "" || req.ChapSecret != "" {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "FC Initiator 不支持 CHAP")
			return
		}
	} else if !reIQN.MatchString(req.IQN) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "IQN 非法")
		return
	}
	if req.ChapSecret != "" {
		if req.ChapUser == "" {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "CHAP 口令需配套用户名")
			return
		}
		if len(req.ChapSecret) < 12 {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "CHAP 口令至少 12 字符")
			return
		}
	}
	enc := ""
	if req.ChapSecret != "" {
		if enc, err = a.chapEncrypt(req.ChapSecret); err != nil {
			writeErr(w, http.StatusInternalServerError, "CRYPTO", "%v", err)
			return
		}
	}
	var iid int64
	err = a.db.QueryRow(r.Context(), `
		INSERT INTO initiators(host_id,transport,iqn,chap_user,chap_secret_enc) VALUES($1,$2,$3,$4,$5)
		RETURNING id`, id, req.Transport, req.IQN, req.ChapUser, enc).Scan(&iid)
	if err != nil {
		writeErr(w, http.StatusConflict, "CONFLICT", "IQN 已存在: %v", err)
		return
	}
	writeOK(w, map[string]any{"initiator_id": iid})
}

func (a *App) handleInitiatorDelete(w http.ResponseWriter, r *http.Request) {
	hid, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	iid, err := parseID(r, "iid")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var mcnt int64
	_ = a.db.QueryRow(r.Context(), `
		SELECT count(*) FROM mappings m JOIN initiators i ON i.host_id=m.host_id
		WHERE i.id=$1`, iid).Scan(&mcnt)
	if mcnt > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "主机存在映射时不能删除其 Initiator(请先解除映射)")
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM initiators WHERE id=$1 AND host_id=$2`, iid, hid); err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	writeOK(w, map[string]any{"deleted": iid})
}
