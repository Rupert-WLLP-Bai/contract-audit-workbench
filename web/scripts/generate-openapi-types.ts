// File metadata (manifest mirror):
// Purpose: Delegates OpenAPI schema compilation to locked openapi-typescript and its AST printer, retaining the WireSchemas alias, schema-derived runtime enums and compatibility CLI modes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import openapiTS, { astToString, type OpenAPI3 } from "openapi-typescript";
import ts from "typescript";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const defaultSchema = resolve(repositoryRoot, "internal/apidocs/openapi.json");
const defaultOutput = resolve(repositoryRoot, "web/src/api/wire.generated.ts");

const ROOTS = [
  "SupportGrant",
  "SupportGrantList",
  "SupportGrantRequest",
  "SupportGrantCommand",
  "SupportCaseView",
  "Session",
  "ActiveContext",
  "ContextList",
  "ContextSwitchRequest",
  "MembershipChoice",
  "PendingMembershipList",
  "PendingMembershipSelectionRequest",
  "Activity",
  "ArchiveRequest",
  "CaseProfile",
  "CaseProfileInput",
  "CaseRelationInput",
  "ContractAcceptance",
  "ContractAcceptanceDefect",
  "ContractAcceptanceDefectInput",
  "ContractAcceptanceList",
  "ContractAcceptanceRequest",
  "ContractSourceCapability",
  "ContractLifecycleOverview",
  "ContractDelivery",
  "ContractDeliveryList",
  "ContractDeliveryRequest",
  "ContractFulfillmentItemSummary",
  "ContractRectification",
  "ContractRectificationRequest",
  "ContractPaymentEligibilityRequest",
  "ContractPaymentEligibility",
  "ContractPaymentReceiptRequest",
  "ContractPaymentReceipt",
  "ContractPaymentSummary",
  "ContractAssetList",
  "ContractAssetPlanSummary",
  "ContractAssetHandover",
  "ContractAssetReceipt",
  "ContractAssetHandoverRequest",
  "ContractAssetReceiptRequest",
  "ContractSigningCheckRequest",
  "ContractSigningCheckResult",
  "ManualSigningComparison",
  "ContractSigningRegistration",
  "ContractSigningRegistrationList",
  "ContractSigningRegistrationRequest",
  "ContractSupplement",
  "ContractSupplementList",
  "ContractSupplementRequest",
  "ContractSupplementResult",
  "CreateGenerationRequest",
  "CreateReportRequest",
  "Decision",
  "DecisionRequest",
  "EvidenceReceiptCreate",
  "EvidenceRequestCreate",
  "EvidenceRequestVerify",
  "ExperienceEvidenceRef",
  "ExperienceEvidenceView",
  "ExperienceFeedbackRequest",
  "FindingCounts",
  "KnowledgeInput",
  "LLMModelSelectionInput",
  "MaterialProjection",
  "MaterialSelectionProjection",
  "MaterialVersionProjection",
  "RemediationInput",
  "RemediationSubmitInput",
  "RemediationVerifyInput",
  "TextOffset",
  "WorkbenchTask",
  "WorkbenchTaskInput",
  "WorkbenchTaskUpdateInput",
  "WorkbenchSnapshotTask",
  "WorkbenchCase",
  "TenantRuntimeControl",
  "TenantExecutionQueue",
  "TenantCostWindow",
  "TenantCost",
  "TenantConfigRef",
  "TenantConfigCatalogEntry",
  "TenantModelCatalogEntry",
  "TenantCredentialRef",
  "TenantConfigSelection",
  "AdoptedTenantConfig",
  "TenantConfigView",
  "TenantConfigPreviewRequest",
  "TenantConfigDiff",
  "TenantConfigValidationError",
  "TenantConfigPreview",
  "TenantConfigAdoptionRequest",
  "TenantConfigAdoptionResult",
  "TenantLiveControlRequest",
  "TenantLiveControlResult",
  "TenantOperations",
  "AdoptedTenantConfigIdentity",
  "BusinessSnapshot",
  "TenantMoneyWindow",
  "TenantMoneyPolicy",
  "TenantMoneyPolicyInput",
  "PlatformModelPriceInput",
  "PlatformModelMoneyPolicyInput",
  "PlatformModelMoneyPolicyResult",
  "PlatformModelMoneyPolicyView",
  "TenantMoneyAdjustmentInput",
  "TenantMoneyAdjustmentResult",
  "TenantProvisionOperationsView",
  "TenantProvisionOperationResult",
  "TenantProvisionInput",
  "TenantProvisionStepInput",
  "JurisdictionOperationsView",
] as const;

// Runtime guards read values from the same schema document as the generated types.
const ENUM_PATHS = [
  ["MaterialProjection", "legacy_status"],
  ["MaterialProjection", "purpose"],
  ["MaterialProjection", "role"],
  ["MaterialSelectionProjection", "status"],
  ["WorkbenchTask", "kind"],
  ["WorkbenchTask", "priority"],
  ["WorkbenchTask", "status"],
] as const;

type JsonObject = Record<string, unknown>;

type Options = {
  schema: string;
  output: string;
  includeAll: boolean;
  check: boolean;
};

function objectValue(value: unknown): JsonObject | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JsonObject)
    : undefined;
}

function decodePointerToken(value: string): string {
  return value.replace(/~1/g, "/").replace(/~0/g, "~");
}

function referencedSchemaName(reference: string): string | undefined {
  if (!reference.startsWith("#/")) {
    return undefined;
  }
  const parts = reference.slice(2).split("/").map(decodePointerToken);
  if (parts[0] === "components" && parts[1] === "schemas" && parts[2]) {
    return parts[2];
  }
  return undefined;
}

function visitSchemaReferences(value: unknown, visit: (name: string) => void): void {
  if (Array.isArray(value)) {
    for (const child of value) {
      visitSchemaReferences(child, visit);
    }
    return;
  }
  const record = objectValue(value);
  if (!record) {
    return;
  }
  if (typeof record.$ref === "string") {
    const name = referencedSchemaName(record.$ref);
    if (name) {
      visit(name);
    }
  }
  for (const child of Object.values(record)) {
    visitSchemaReferences(child, visit);
  }
}

function selectedSchemas(document: JsonObject, includeAll: boolean): Record<string, unknown> {
  const components = objectValue(document.components);
  const schemas = objectValue(components?.schemas);
  if (!schemas || Object.keys(schemas).length === 0) {
    throw new Error("components.schemas must be a nonempty object");
  }
  if (includeAll) {
    return Object.fromEntries(
      Object.entries(schemas).sort(([left], [right]) => left.localeCompare(right)),
    );
  }

  const pending: string[] = [...ROOTS];
  const included = new Set<string>();
  while (pending.length > 0) {
    const name = pending.pop();
    if (!name || included.has(name)) {
      continue;
    }
    if (!Object.hasOwn(schemas, name)) {
      throw new Error("missing root schema " + JSON.stringify(name));
    }
    included.add(name);
    visitSchemaReferences(schemas[name], (referenced) => pending.push(referenced));
  }
  return Object.fromEntries(
    [...included]
      .sort((left, right) => left.localeCompare(right))
      .map((name) => [name, schemas[name]]),
  );
}

function makeWireEnums(schemas: Record<string, unknown>): ts.VariableStatement {
  const enumProperties = ENUM_PATHS.map(([schemaName, fieldName]) => {
    const schema = objectValue(schemas[schemaName]);
    const properties = objectValue(schema?.properties);
    const field = objectValue(properties?.[fieldName]);
    const values = field?.enum;
    if (
      !Array.isArray(values) ||
      values.length === 0 ||
      !values.every((value) => typeof value === "string")
    ) {
      throw new Error(
        schemaName + "." + fieldName + ": expected a nonempty string enum in the OpenAPI schema",
      );
    }
    return ts.factory.createPropertyAssignment(
      ts.factory.createStringLiteral(schemaName + "." + fieldName),
      ts.factory.createArrayLiteralExpression(
        values.map((value) => ts.factory.createStringLiteral(value as string)),
        false,
      ),
    );
  });

  const value = ts.factory.createAsExpression(
    ts.factory.createObjectLiteralExpression(enumProperties, true),
    ts.factory.createTypeReferenceNode(ts.factory.createIdentifier("const"), undefined),
  );
  const declaration = ts.factory.createVariableDeclaration(
    "wireEnums",
    undefined,
    undefined,
    value,
  );
  return ts.factory.createVariableStatement(
    [ts.factory.createModifier(ts.SyntaxKind.ExportKeyword)],
    ts.factory.createVariableDeclarationList([declaration], ts.NodeFlags.Const),
  );
}

function makeWireSchemasAlias(): ts.TypeAliasDeclaration {
  return ts.factory.createTypeAliasDeclaration(
    [ts.factory.createModifier(ts.SyntaxKind.ExportKeyword)],
    "WireSchemas",
    undefined,
    ts.factory.createIndexedAccessTypeNode(
      ts.factory.createTypeReferenceNode("components", undefined),
      ts.factory.createLiteralTypeNode(ts.factory.createStringLiteral("schemas")),
    ),
  );
}

function parseArguments(argv: string[]): Options {
  const options: Options = {
    schema: defaultSchema,
    output: defaultOutput,
    includeAll: false,
    check: false,
  };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--all") {
      options.includeAll = true;
    } else if (argument === "--check") {
      options.check = true;
    } else if (argument === "--schema" || argument === "--output" || argument === "-o") {
      const value = argv[index + 1];
      if (!value || value.startsWith("--")) {
        throw new Error(argument + " requires a path");
      }
      index += 1;
      if (argument === "--schema") {
        options.schema = resolve(process.cwd(), value);
      } else {
        options.output = resolve(process.cwd(), value);
      }
    } else if (argument.startsWith("--schema=")) {
      options.schema = resolve(process.cwd(), argument.slice("--schema=".length));
    } else if (argument.startsWith("--output=")) {
      options.output = resolve(process.cwd(), argument.slice("--output=".length));
    } else if (argument === "--help" || argument === "-h") {
      console.log(
        "Usage: bun run generate-openapi-types [--schema PATH] [--output PATH] [--all] [--check]",
      );
      process.exit(0);
    } else {
      throw new Error("unknown argument " + JSON.stringify(argument));
    }
  }
  return options;
}

async function main(): Promise<void> {
  const options = parseArguments(process.argv.slice(2));
  const source = await readFile(options.schema, "utf8");
  const document = objectValue(JSON.parse(source));
  if (!document || !String(document.openapi ?? "").startsWith("3.")) {
    throw new Error("expected an OpenAPI 3 document");
  }

  const schemas = selectedSchemas(document, options.includeAll);
  const input: OpenAPI3 = {
    ...document,
    paths: {},
    webhooks: {},
    components: { schemas },
  } as OpenAPI3;
  const ast = await openapiTS(input, {
    alphabetize: true,
    cwd: dirname(options.schema),
    emptyObjectsUnknown: true,
  });
  const generated = astToString([...ast, makeWireSchemasAlias(), makeWireEnums(schemas)]);
  const content = [
    "// Generated by openapi-typescript from internal/apidocs/openapi.json.",
    "// These are wire types only; runtime guards and the server validate the data.",
    generated,
  ].join("\n");

  if (options.check) {
    let current: string;
    try {
      current = await readFile(options.output, "utf8");
    } catch {
      throw new Error(options.output + ": generated wire types are missing");
    }
    if (current !== content) {
      throw new Error(options.output + ": generated wire types are stale");
    }
    return;
  }
  await mkdir(dirname(options.output), { recursive: true });
  await writeFile(options.output, content, "utf8");
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error("OpenAPI wire generation failed: " + message);
  process.exitCode = 1;
});
