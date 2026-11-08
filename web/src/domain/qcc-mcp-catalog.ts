// File metadata (manifest mirror):
// Purpose: Defines the read-only Qichacha MCP server and skill catalog used by the demo integration panel; the catalog identifies provider skills but does not execute or authorize calls.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

export interface QccMcpServer {
  server: string;
  title: string;
  version: string;
  status: "RECOGNIZED" | "UNAUTHORIZED" | "ERROR";
  skillCount: number;
  error: string;
  skills: string[];
}

export const qccMcpServers: QccMcpServer[] = [
  {
    "server": "company",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 16,
    "error": "",
    "skills": [
      "get_actual_controller",
      "get_annual_reports",
      "get_beneficial_owners",
      "get_branches",
      "get_change_records",
      "get_company_by_query",
      "get_company_profile",
      "get_company_registration_info",
      "get_contact_info",
      "get_external_investments",
      "get_financial_data",
      "get_key_personnel",
      "get_listing_info",
      "get_shareholder_info",
      "get_tax_invoice_info",
      "verify_company_accuracy"
    ]
  },
  {
    "server": "risk",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 38,
    "error": "",
    "skills": [
      "get_administrative_penalty",
      "get_bankruptcy_reorganization",
      "get_business_exception",
      "get_cancellation_record_info",
      "get_case_filing_info",
      "get_chattel_mortgage_info",
      "get_company_related_risk_scan",
      "get_company_risk_scan",
      "get_court_notice",
      "get_default_info",
      "get_disciplinary_list",
      "get_dishonest_info",
      "get_environmental_penalty",
      "get_equity_freeze",
      "get_equity_pledge_info",
      "get_exit_restriction",
      "get_guarantee_info",
      "get_hearing_notice",
      "get_high_consumption_restriction",
      "get_judgment_debtor_info",
      "get_judicial_auction",
      "get_judicial_document_detail",
      "get_judicial_documents",
      "get_land_mortgage_info",
      "get_liquidation_info",
      "get_pre_litigation_mediation",
      "get_property_asset_announcement",
      "get_public_exhortation",
      "get_serious_violation",
      "get_service_announcement",
      "get_service_notice",
      "get_simple_cancellation_info",
      "get_stock_pledge_info",
      "get_tax_abnormal",
      "get_tax_arrears_notice",
      "get_tax_violation",
      "get_terminated_cases",
      "get_valuation_inquiry"
    ]
  },
  {
    "server": "ipr",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 18,
    "error": "",
    "skills": [
      "get_app_info",
      "get_commercial_franchise",
      "get_copyright_work_info",
      "get_douyin_account",
      "get_integrated_circuit_layout",
      "get_international_patent",
      "get_internet_service_info",
      "get_ipr_pledge",
      "get_kuaishou_account",
      "get_mini_program",
      "get_online_store",
      "get_patent_info",
      "get_software_copyright_info",
      "get_standard_info",
      "get_trademark_document",
      "get_trademark_info",
      "get_wechat_official_account",
      "get_weibo_account"
    ]
  },
  {
    "server": "operation",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 36,
    "error": "",
    "skills": [
      "get_administrative_license",
      "get_advertising_review",
      "get_asset_auction",
      "get_bidding_info",
      "get_bond_financing_info",
      "get_company_announcement",
      "get_counterfeit_cosmetics",
      "get_credit_commitments",
      "get_credit_evaluation",
      "get_entry_denied",
      "get_financing_lease_info",
      "get_financing_records",
      "get_food_safety",
      "get_game_approval",
      "get_government_announcement",
      "get_government_interview",
      "get_honor_info",
      "get_import_export_credit",
      "get_investment_institution",
      "get_land_grant_info",
      "get_land_transfer_info",
      "get_news_sentiment",
      "get_private_fund_manager",
      "get_product_recall",
      "get_product_spot_check",
      "get_property_rights_transaction",
      "get_qualifications",
      "get_random_check",
      "get_ranking_list_info",
      "get_recruitment_info",
      "get_related_announcement",
      "get_software_violation",
      "get_spot_check_info",
      "get_taxpayer_qualification",
      "get_tech_achievement",
      "get_telecom_license"
    ]
  },
  {
    "server": "history",
    "title": "history",
    "version": "",
    "status": "ERROR",
    "skillCount": 0,
    "error": "mcp error 300012: 当前用户未授权使用历史信息服务",
    "skills": []
  },
  {
    "server": "executive",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 44,
    "error": "",
    "skills": [
      "get_executive_admin_penalty",
      "get_executive_beneficial_owner",
      "get_executive_case_filing",
      "get_executive_controlled_companies",
      "get_executive_court_notice",
      "get_executive_dishonest",
      "get_executive_equity_freeze",
      "get_executive_equity_pledge",
      "get_executive_exit_restriction",
      "get_executive_hearing_notice",
      "get_executive_high_consumption_ban",
      "get_executive_historical_admin_penalty",
      "get_executive_historical_case_filing",
      "get_executive_historical_court_notice",
      "get_executive_historical_dishonest",
      "get_executive_historical_equity_freeze",
      "get_executive_historical_equity_pledge",
      "get_executive_historical_hearing_notice",
      "get_executive_historical_high_consumption_ban",
      "get_executive_historical_investments",
      "get_executive_historical_judgment_debtor",
      "get_executive_historical_judicial_docs",
      "get_executive_historical_legal_rep_roles",
      "get_executive_historical_partners",
      "get_executive_historical_positions",
      "get_executive_historical_pre_litigation_mediation",
      "get_executive_historical_related_companies",
      "get_executive_historical_service_notice",
      "get_executive_historical_terminated_cases",
      "get_executive_investments",
      "get_executive_judgment_debtor",
      "get_executive_judicial_docs",
      "get_executive_legal_rep_roles",
      "get_executive_positions",
      "get_executive_pre_litigation_mediation",
      "get_executive_property_reward_notice",
      "get_executive_related_companies",
      "get_executive_related_risk_scan",
      "get_executive_risk_scan",
      "get_executive_service_notice",
      "get_executive_stock_pledge",
      "get_executive_tax_violation",
      "get_executive_terminated_cases",
      "get_executive_valuation_inquiry"
    ]
  },
  {
    "server": "regulation",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 6,
    "error": "",
    "skills": [
      "get_legal_article_detail",
      "get_legal_article_search",
      "get_legal_article_semantic_search",
      "get_legal_citation_verify",
      "get_legal_regulation_detail",
      "get_legal_regulation_search"
    ]
  },
  {
    "server": "case",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 4,
    "error": "",
    "skills": [
      "get_judicial_authoritative_case_search",
      "get_judicial_case_detail",
      "get_judicial_case_search",
      "get_judicial_citation_verify"
    ]
  },
  {
    "server": "tender",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 6,
    "error": "",
    "skills": [
      "get_proposed_project_detail",
      "get_tender_detail",
      "search_companies",
      "search_company_tenders",
      "search_proposed_projects",
      "search_tenders"
    ]
  },
  {
    "server": "document",
    "title": "QCC MCP",
    "version": "1.0.0",
    "status": "RECOGNIZED",
    "skillCount": 2,
    "error": "",
    "skills": [
      "get_parse_result",
      "parse_document"
    ]
  }
];
