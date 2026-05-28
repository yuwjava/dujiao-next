package repository

import (
	"errors"
	"strings"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"

	"gorm.io/gorm"
)

// UserRepository 用户数据访问接口
type UserRepository interface {
	GetByEmail(email string) (*models.User, error)
	GetByID(id uint) (*models.User, error)
	ListByIDs(ids []uint) ([]models.User, error)
	Create(user *models.User) error
	Update(user *models.User) error
	List(filter UserListFilter) ([]models.User, int64, error)
	BatchUpdateStatus(userIDs []uint, status string) error
	AssignDefaultMemberLevel(defaultLevelID uint) (int64, error)

	// TOTP 相关
	UpdateTOTPPending(userID uint, encSecret string, expiresAt time.Time) error
	UpdateTOTPEnabled(userID uint, encSecret string, enabledAt time.Time, recoveryCodesJSON string) error
	UpdateRecoveryCodes(userID uint, recoveryCodesJSON string) error
	ClearTOTP(userID uint) error
}

// GormUserRepository GORM 实现
type GormUserRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓库
func NewUserRepository(db *gorm.DB) *GormUserRepository {
	return &GormUserRepository{db: db}
}

// GetByEmail 根据邮箱获取用户
func (r *GormUserRepository) GetByEmail(email string) (*models.User, error) {
	var user models.User
	if err := r.db.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// GetByID 根据 ID 获取用户
func (r *GormUserRepository) GetByID(id uint) (*models.User, error) {
	var user models.User
	if err := r.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// ListByIDs 批量获取用户
func (r *GormUserRepository) ListByIDs(ids []uint) ([]models.User, error) {
	if len(ids) == 0 {
		return []models.User{}, nil
	}
	var users []models.User
	if err := r.db.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// Create 创建用户
func (r *GormUserRepository) Create(user *models.User) error {
	return r.db.Create(user).Error
}

// Update 更新用户
func (r *GormUserRepository) Update(user *models.User) error {
	return r.db.Save(user).Error
}

// List 用户列表
func (r *GormUserRepository) List(filter UserListFilter) ([]models.User, int64, error) {
	query := r.db.Model(&models.User{})

	if filter.UserID != 0 {
		query = query.Where("users.id = ?", filter.UserID)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where(
			"email LIKE ? OR display_name LIKE ? OR EXISTS ("+
				"SELECT 1 FROM user_oauth_identities "+
				"WHERE user_oauth_identities.user_id = users.id "+
				"AND ("+
				"user_oauth_identities.provider LIKE ? OR "+
				"user_oauth_identities.provider_user_id LIKE ? OR "+
				"user_oauth_identities.username LIKE ?"+
				")"+
				")",
			like, like, like, like, like,
		)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.CreatedFrom != nil {
		query = query.Where("created_at >= ?", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		query = query.Where("created_at <= ?", *filter.CreatedTo)
	}
	if filter.LastLoginFrom != nil {
		query = query.Where("last_login_at >= ?", *filter.LastLoginFrom)
	}
	if filter.LastLoginTo != nil {
		query = query.Where("last_login_at <= ?", *filter.LastLoginTo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query = applyPagination(query, filter.Page, filter.PageSize)

	var users []models.User
	if err := query.Order("id DESC").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// BatchUpdateStatus 批量更新用户状态
func (r *GormUserRepository) BatchUpdateStatus(userIDs []uint, status string) error {
	if len(userIDs) == 0 {
		return nil
	}
	now := time.Now()
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": now,
	}
	if strings.ToLower(strings.TrimSpace(status)) == constants.UserStatusDisabled {
		updates["token_invalid_before"] = now
		updates["token_version"] = gorm.Expr("token_version + 1")
	}
	return r.db.Model(&models.User{}).Where("id IN ?", userIDs).Updates(updates).Error
}

// AssignDefaultMemberLevel 为所有未分配等级(member_level_id=0)的用户批量分配默认等级
func (r *GormUserRepository) AssignDefaultMemberLevel(defaultLevelID uint) (int64, error) {
	if defaultLevelID == 0 {
		return 0, nil
	}
	result := r.db.Model(&models.User{}).
		Where("member_level_id = 0 OR member_level_id IS NULL").
		Updates(map[string]interface{}{
			"member_level_id": defaultLevelID,
			"updated_at":      time.Now(),
		})
	return result.RowsAffected, result.Error
}

// UpdateTOTPPending 写入待绑定 secret 与过期时间
func (r *GormUserRepository) UpdateTOTPPending(userID uint, encSecret string, expiresAt time.Time) error {
	if userID == 0 {
		return errors.New("invalid user id")
	}
	return r.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_pending_secret":     encSecret,
		"totp_pending_expires_at": expiresAt,
	}).Error
}

// UpdateTOTPEnabled 完成绑定：迁移 pending → 正式 secret，写入恢复码，清空 pending；
// 同时 TokenVersion++ 强制其他设备的旧 session 失效（提升安全级别等同于改密码）。
func (r *GormUserRepository) UpdateTOTPEnabled(userID uint, encSecret string, enabledAt time.Time, recoveryCodesJSON string) error {
	if userID == 0 {
		return errors.New("invalid user id")
	}
	return r.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_secret":             encSecret,
		"totp_enabled_at":         enabledAt,
		"totp_pending_secret":     "",
		"totp_pending_expires_at": nil,
		"recovery_codes":          recoveryCodesJSON,
		"token_version":           gorm.Expr("token_version + 1"),
		"token_invalid_before":    enabledAt,
	}).Error
}

// UpdateRecoveryCodes 替换恢复码 JSON
func (r *GormUserRepository) UpdateRecoveryCodes(userID uint, recoveryCodesJSON string) error {
	if userID == 0 {
		return errors.New("invalid user id")
	}
	return r.db.Model(&models.User{}).Where("id = ?", userID).Update("recovery_codes", recoveryCodesJSON).Error
}

// ClearTOTP 清空所有 TOTP 字段，TokenVersion++ 强制下线
func (r *GormUserRepository) ClearTOTP(userID uint) error {
	if userID == 0 {
		return errors.New("invalid user id")
	}
	now := time.Now()
	return r.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_secret":             "",
		"totp_enabled_at":         nil,
		"totp_pending_secret":     "",
		"totp_pending_expires_at": nil,
		"recovery_codes":          "",
		"token_version":           gorm.Expr("token_version + 1"),
		"token_invalid_before":    now,
	}).Error
}
