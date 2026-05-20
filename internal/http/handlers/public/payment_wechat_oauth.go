package public

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/http/handlers/shared"
	"github.com/dujiao-next/internal/http/response"
	"github.com/dujiao-next/internal/payment/wechatpay"

	"github.com/gin-gonic/gin"
)

type WechatOAuthAuthorizeQuery struct {
	ChannelID   uint   `form:"channel_id" binding:"required"`
	RedirectURI string `form:"redirect_uri" binding:"required"`
}

type WechatOAuthCallbackQuery struct {
	Code  string `form:"code"`
	State string `form:"state"`
}

type WechatOAuthState struct {
	ChannelID   uint   `json:"channel_id"`
	RedirectURI string `json:"redirect_uri"`
}

// WechatOAuthAuthorize 处理微信网页授权入口
func (h *Handler) WechatOAuthAuthorize(c *gin.Context) {
	var query WechatOAuthAuthorizeQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		shared.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}

	channel, err := h.PaymentService.GetChannel(query.ChannelID)
	if err != nil {
		shared.RespondError(c, response.CodeNotFound, "error.payment_channel_not_found", nil)
		return
	}

	if channel.ProviderType != constants.PaymentProviderOfficial || channel.ChannelType != constants.PaymentChannelTypeWechat {
		shared.RespondError(c, response.CodeBadRequest, "error.payment_channel_invalid", nil)
		return
	}

	config, err := wechatpay.ParseConfig(channel.ConfigJSON)
	if err != nil {
		shared.RespondError(c, response.CodeInternal, "error.payment_channel_config_invalid", err)
		return
	}
	config.Normalize()

	if config.AppID == "" {
		shared.RespondError(c, response.CodeInternal, "error.payment_channel_config_invalid", nil)
		return
	}

	stateRaw, _ := json.Marshal(WechatOAuthState{
		ChannelID:   query.ChannelID,
		RedirectURI: query.RedirectURI,
	})
	state := base64.URLEncoding.EncodeToString(stateRaw)

	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callbackURL := fmt.Sprintf("%s://%s/api/v1/public/payment/wechat/oauth2/callback", scheme, c.Request.Host)

	authURL := fmt.Sprintf("https://open.weixin.qq.com/connect/oauth2/authorize?appid=%s&redirect_uri=%s&response_type=code&scope=snsapi_base&state=%s#wechat_redirect",
		config.AppID,
		url.QueryEscape(callbackURL),
		state,
	)

	c.Redirect(http.StatusFound, authURL)
}

// WechatOAuthCallback 处理微信网页授权回调
func (h *Handler) WechatOAuthCallback(c *gin.Context) {
	var query WechatOAuthCallbackQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		shared.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}

	stateRaw, err := base64.URLEncoding.DecodeString(query.State)
	if err != nil {
		shared.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}

	var state WechatOAuthState
	if err := json.Unmarshal(stateRaw, &state); err != nil {
		shared.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}

	if query.Code == "" {
		// User rejected or error
		c.Redirect(http.StatusFound, state.RedirectURI)
		return
	}

	channel, err := h.PaymentService.GetChannel(state.ChannelID)
	if err != nil {
		shared.RespondError(c, response.CodeNotFound, "error.payment_channel_not_found", nil)
		return
	}

	config, err := wechatpay.ParseConfig(channel.ConfigJSON)
	if err != nil {
		shared.RespondError(c, response.CodeInternal, "error.payment_channel_config_invalid", err)
		return
	}
	config.Normalize()

	if config.AppID == "" || config.AppSecret == "" {
		shared.RespondError(c, response.CodeInternal, "error.payment_channel_config_invalid", nil)
		return
	}

	tokenURL := fmt.Sprintf("https://api.weixin.qq.com/sns/oauth2/access_token?appid=%s&secret=%s&code=%s&grant_type=authorization_code",
		config.AppID,
		config.AppSecret,
		query.Code,
	)

	resp, err := http.Get(tokenURL)
	if err != nil {
		shared.RespondError(c, response.CodeInternal, "error.payment_create_failed", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		shared.RespondError(c, response.CodeInternal, "error.payment_create_failed", err)
		return
	}

	var result struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		shared.RespondError(c, response.CodeInternal, "error.payment_create_failed", err)
		return
	}

	if result.ErrCode != 0 || result.OpenID == "" {
		// failed to get openid
		c.Redirect(http.StatusFound, state.RedirectURI)
		return
	}

	redirectURL, err := url.Parse(state.RedirectURI)
	if err != nil {
		shared.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}

	q := redirectURL.Query()
	q.Set("openid", result.OpenID)
	redirectURL.RawQuery = q.Encode()

	c.Redirect(http.StatusFound, redirectURL.String())
}
