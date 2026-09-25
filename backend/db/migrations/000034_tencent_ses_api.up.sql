-- Personal Tencent Cloud SES accounts no longer rely on SMTP. Remove legacy
-- channel fields and make the API provider explicit; credentials and template
-- IDs remain empty until configured through ops or deployment secrets.
UPDATE ops_config
   SET value = (value
                - 'smtpHost' - 'smtpPort' - 'smtpTlsMode' - 'smtpUsername'
                - 'smtpFrom' - 'smtpFromName' - 'smtpPassword')
             || jsonb_build_object(
                    'provider', 'tencent_ses',
                    'sesRegion', COALESCE(NULLIF(value->>'sesRegion', ''), 'ap-guangzhou'),
                    'sesTemplateIds', COALESCE(value->'sesTemplateIds', '{}'::jsonb)
                ),
       updated_at = now()
 WHERE key = 'mail';
