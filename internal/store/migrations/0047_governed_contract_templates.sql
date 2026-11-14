ALTER TABLE governed_knowledge_versions DROP CONSTRAINT governed_knowledge_versions_kind_check;
ALTER TABLE governed_knowledge_versions ADD CONSTRAINT governed_knowledge_versions_kind_check
 CHECK (kind IN ('policy','guidance','exception','experience','legal','template'));
ALTER TABLE business_generations ADD COLUMN template_digest text;
ALTER TABLE business_generations ADD CONSTRAINT business_generations_template_digest_check
 CHECK (template_digest IS NULL OR template_digest ~ '^[0-9a-f]{64}$');
