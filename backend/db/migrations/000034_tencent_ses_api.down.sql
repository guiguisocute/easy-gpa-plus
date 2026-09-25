UPDATE ops_config
   SET value = (value
                - 'sesRegion' - 'sesSecretId' - 'sesSecretKey' - 'sesFrom'
                - 'sesFromName' - 'sesReplyTo' - 'sesTemplateIds')
             || jsonb_build_object('provider', 'smtp'),
       updated_at = now()
 WHERE key = 'mail';
