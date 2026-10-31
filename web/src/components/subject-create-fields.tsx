import { Alert, Form, Input } from "antd";
import type { SubjectCreateInput } from "../api/types";

export interface SubjectCreateFormValues {
  display_name?: string;
  legal_name?: string;
  identifier_value?: string;
}

const usccPattern = /^[0-9A-HJ-NP-RTUW-Y]{18}$/;

export function SubjectCreateFields() {
  return (
    <>
      <Form.Item
        label="主体名称"
        name="display_name"
        rules={[{ required: true, whitespace: true, message: "请填写主体名称" }]}
      >
        <Input autoComplete="off" maxLength={200} />
      </Form.Item>
      <Form.Item label="法定名称（可选）" name="legal_name">
        <Input autoComplete="off" maxLength={200} />
      </Form.Item>
      <Form.Item
        label="统一社会信用代码（可选）"
        name="identifier_value"
        rules={[{
          validator: async (_rule, value: unknown) => {
            if (typeof value !== "string" || value.trim() === "") return;
            if (!usccPattern.test(value.trim().toUpperCase()))
              throw new Error("请输入18位统一社会信用代码；格式正确不代表身份已核验");
          },
        }]}
      >
        <Input autoComplete="off" maxLength={18} />
      </Form.Item>
      <Alert
        type="info"
        showIcon
        title="新主体默认待核验。名称和标识由经办人录入，不会自动匹配或合并其他主体。"
      />
    </>
  );
}

export function subjectCreateInput(values: SubjectCreateFormValues): SubjectCreateInput {
  const displayName = values.display_name?.trim() ?? "";
  if (displayName === "") throw new Error("请填写主体名称");
  const legalName = values.legal_name?.trim() ?? "";
  const identifierValue = values.identifier_value?.trim().toUpperCase() ?? "";
  if (identifierValue !== "" && !usccPattern.test(identifierValue))
    throw new Error("请输入18位统一社会信用代码；格式正确不代表身份已核验");
  return {
    display_name: displayName,
    ...(legalName === "" ? {} : { legal_name: legalName }),
    ...(identifierValue === ""
      ? {}
      : {
          identifiers: [{
            namespace: "cn.gov.samr.uscc",
            identifier_type: "unified_social_credit_code",
            value: identifierValue,
          }],
        }),
  };
}
