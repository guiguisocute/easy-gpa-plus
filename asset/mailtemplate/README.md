# 邮件模板

腾讯云 SES 通道使用的 HTML 模板。新部署只需申请 `verification_code`、`password_reset`、`mail_test`、`notification_alert` 与 `notification_digest`；其他文件只为兼容历史记录保留，对应的发送路径已停用。

`https://gpa.example.org` 是占位地址，运行时由 `PUBLIC_URL` 替换；上传到 SES 控制台的副本需要先改成自己的 HTTPS 站点地址。

变量、配置与启用步骤见文档站的[邮件通知](https://easygpa.guiguisocute.com/deploy/mail)。
