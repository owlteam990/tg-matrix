package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

func newMarketingID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MarketingProduct 营销产品内容。
type MarketingProduct struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SellingPoints string `json:"sellingPoints"`
	Audience     string `json:"audience"`
	AvoidAudience string `json:"avoidAudience"`
	Offer        string `json:"offer"`
	LandingURL   string `json:"landingUrl"`
	Tone         string `json:"tone"`
	BannedWords  string `json:"bannedWords"`
	CTA          string `json:"cta"`
	Status       string `json:"status"` // draft|active|disabled
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// MarketingLead 营销线索（发言者 → AI → 审核）。
type MarketingLead struct {
	ID            string  `json:"id"`
	ProductID     string  `json:"productId"`
	AccountID     string  `json:"accountId"`
	GroupTarget   string  `json:"groupTarget"`
	GroupTitle    string  `json:"groupTitle"`
	UserID        int64   `json:"userId"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"displayName"`
	Digest        string  `json:"digest"`
	FitScore      int     `json:"fitScore"`
	FitLabel      string  `json:"fitLabel"`
	FitReasons    string  `json:"fitReasons"`
	RiskFlags     string  `json:"riskFlags"`
	ScriptA       string  `json:"scriptA"`
	ScriptB       string  `json:"scriptB"`
	ScriptChosen  string  `json:"scriptChosen"`
	Status        string  `json:"status"` // pending|analyzing|analyzed|scripting|scripted|approved|sent|skipped|rejected
	SendAccountID string  `json:"sendAccountId"`
	CloudCallIDs  string  `json:"cloudCallIds"`
	ErrorMessage  string  `json:"errorMessage"`
	CreatedAt     int64   `json:"createdAt"`
	UpdatedAt     int64   `json:"updatedAt"`
}

/**
 * 列出营销产品（新→旧）。
 *
 * @return 产品列表
 */
func (s *Store) ListMarketingProducts() ([]MarketingProduct, error) {
	rows, err := s.DB.Query(`
SELECT id, name, selling_points, audience, avoid_audience, offer, landing_url,
       tone, banned_words, cta, status, created_at, updated_at
FROM marketing_products ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MarketingProduct, 0)
	for rows.Next() {
		var p MarketingProduct
		if err := rows.Scan(
			&p.ID, &p.Name, &p.SellingPoints, &p.Audience, &p.AvoidAudience, &p.Offer, &p.LandingURL,
			&p.Tone, &p.BannedWords, &p.CTA, &p.Status, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

/**
 * 按 ID 取产品。
 *
 * @param id 产品 ID
 * @return 产品
 */
func (s *Store) GetMarketingProduct(id string) (*MarketingProduct, error) {
	row := s.DB.QueryRow(`
SELECT id, name, selling_points, audience, avoid_audience, offer, landing_url,
       tone, banned_words, cta, status, created_at, updated_at
FROM marketing_products WHERE id=?`, id)
	var p MarketingProduct
	err := row.Scan(
		&p.ID, &p.Name, &p.SellingPoints, &p.Audience, &p.AvoidAudience, &p.Offer, &p.LandingURL,
		&p.Tone, &p.BannedWords, &p.CTA, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

/**
 * 创建或更新营销产品。
 *
 * @param p 产品（空 ID 则新建）
 * @return 保存后的产品
 */
func (s *Store) UpsertMarketingProduct(p MarketingProduct) (MarketingProduct, error) {
	now := time.Now().Unix()
	if p.ID == "" {
		p.ID = newMarketingID()
		p.CreatedAt = now
	}
	if p.Status == "" {
		p.Status = "draft"
	}
	p.UpdatedAt = now
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	_, err := s.DB.Exec(`
INSERT INTO marketing_products (
  id, name, selling_points, audience, avoid_audience, offer, landing_url,
  tone, banned_words, cta, status, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name,
  selling_points=excluded.selling_points,
  audience=excluded.audience,
  avoid_audience=excluded.avoid_audience,
  offer=excluded.offer,
  landing_url=excluded.landing_url,
  tone=excluded.tone,
  banned_words=excluded.banned_words,
  cta=excluded.cta,
  status=excluded.status,
  updated_at=excluded.updated_at
`, p.ID, p.Name, p.SellingPoints, p.Audience, p.AvoidAudience, p.Offer, p.LandingURL,
		p.Tone, p.BannedWords, p.CTA, p.Status, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return p, fmt.Errorf("upsert marketing product: %w", err)
	}
	return p, nil
}

/**
 * 删除营销产品（级联线索由调用方或 FK 处理）。
 *
 * @param id 产品 ID
 */
func (s *Store) DeleteMarketingProduct(id string) error {
	_, err := s.DB.Exec(`DELETE FROM marketing_leads WHERE product_id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM marketing_products WHERE id=?`, id)
	return err
}

/**
 * 批量插入线索（同产品+账号+用户去重更新 digest）。
 *
 * @param leads 线索
 * @return 写入条数
 */
func (s *Store) UpsertMarketingLeads(leads []MarketingLead) (int, error) {
	if len(leads) == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
INSERT INTO marketing_leads (
  id, product_id, account_id, group_target, group_title, user_id, username, display_name,
  digest, fit_score, fit_label, fit_reasons, risk_flags, script_a, script_b, script_chosen,
  status, send_account_id, cloud_call_ids, error_message, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(product_id, account_id, user_id) DO UPDATE SET
  group_target=excluded.group_target,
  group_title=excluded.group_title,
  username=excluded.username,
  display_name=excluded.display_name,
  digest=CASE WHEN length(excluded.digest)>0 THEN excluded.digest ELSE marketing_leads.digest END,
  updated_at=excluded.updated_at
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	for _, lead := range leads {
		if lead.ID == "" {
			lead.ID = newMarketingID()
		}
		if lead.Status == "" {
			lead.Status = "pending"
		}
		if lead.CreatedAt == 0 {
			lead.CreatedAt = now
		}
		lead.UpdatedAt = now
		if _, err := stmt.Exec(
			lead.ID, lead.ProductID, lead.AccountID, lead.GroupTarget, lead.GroupTitle,
			lead.UserID, lead.Username, lead.DisplayName, lead.Digest,
			lead.FitScore, lead.FitLabel, lead.FitReasons, lead.RiskFlags,
			lead.ScriptA, lead.ScriptB, lead.ScriptChosen, lead.Status,
			lead.SendAccountID, lead.CloudCallIDs, lead.ErrorMessage,
			lead.CreatedAt, lead.UpdatedAt,
		); err != nil {
			return n, err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return n, err
	}
	return n, nil
}

/**
 * 列出某产品下线索。
 *
 * @param productID 产品 ID
 * @param status 可选状态过滤
 * @return 线索列表
 */
func (s *Store) ListMarketingLeads(productID, status string) ([]MarketingLead, error) {
	q := `
SELECT id, product_id, account_id, group_target, group_title, user_id, username, display_name,
       digest, fit_score, fit_label, fit_reasons, risk_flags, script_a, script_b, script_chosen,
       status, send_account_id, cloud_call_ids, error_message, created_at, updated_at
FROM marketing_leads WHERE product_id=?`
	args := []any{productID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY fit_score DESC, updated_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MarketingLead, 0)
	for rows.Next() {
		var l MarketingLead
		if err := rows.Scan(
			&l.ID, &l.ProductID, &l.AccountID, &l.GroupTarget, &l.GroupTitle, &l.UserID, &l.Username, &l.DisplayName,
			&l.Digest, &l.FitScore, &l.FitLabel, &l.FitReasons, &l.RiskFlags, &l.ScriptA, &l.ScriptB, &l.ScriptChosen,
			&l.Status, &l.SendAccountID, &l.CloudCallIDs, &l.ErrorMessage, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

/**
 * 按 ID 取线索。
 *
 * @param id 线索 ID
 * @return 线索
 */
func (s *Store) GetMarketingLead(id string) (*MarketingLead, error) {
	row := s.DB.QueryRow(`
SELECT id, product_id, account_id, group_target, group_title, user_id, username, display_name,
       digest, fit_score, fit_label, fit_reasons, risk_flags, script_a, script_b, script_chosen,
       status, send_account_id, cloud_call_ids, error_message, created_at, updated_at
FROM marketing_leads WHERE id=?`, id)
	var l MarketingLead
	err := row.Scan(
		&l.ID, &l.ProductID, &l.AccountID, &l.GroupTarget, &l.GroupTitle, &l.UserID, &l.Username, &l.DisplayName,
		&l.Digest, &l.FitScore, &l.FitLabel, &l.FitReasons, &l.RiskFlags, &l.ScriptA, &l.ScriptB, &l.ScriptChosen,
		&l.Status, &l.SendAccountID, &l.CloudCallIDs, &l.ErrorMessage, &l.CreatedAt, &l.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

/**
 * 更新线索 AI / 审核字段。
 *
 * @param lead 线索
 */
func (s *Store) UpdateMarketingLead(lead MarketingLead) error {
	lead.UpdatedAt = time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE marketing_leads SET
  digest=?, fit_score=?, fit_label=?, fit_reasons=?, risk_flags=?,
  script_a=?, script_b=?, script_chosen=?, status=?, send_account_id=?,
  cloud_call_ids=?, error_message=?, updated_at=?
WHERE id=?`,
		lead.Digest, lead.FitScore, lead.FitLabel, lead.FitReasons, lead.RiskFlags,
		lead.ScriptA, lead.ScriptB, lead.ScriptChosen, lead.Status, lead.SendAccountID,
		lead.CloudCallIDs, lead.ErrorMessage, lead.UpdatedAt, lead.ID,
	)
	return err
}
