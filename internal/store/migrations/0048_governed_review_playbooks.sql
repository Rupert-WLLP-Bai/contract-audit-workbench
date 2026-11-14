ALTER TABLE governed_knowledge_versions DROP CONSTRAINT governed_knowledge_versions_kind_check;
ALTER TABLE governed_knowledge_versions ADD CONSTRAINT governed_knowledge_versions_kind_check
 CHECK (kind IN ('policy','guidance','exception','experience','legal','template','playbook'));
