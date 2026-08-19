package pdf

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"strings"
)

type InvoiceData struct {
	IssuerName     string
	IssuerAddress  string
	IssuerContact  string
	IssuerEmail    string
	IssuerGSTIN    string
	IssuerPAN      string
	InvoiceNumber  string
	InvoiceDate    string
	ClientName     string
	ClientAddress  string
	ClientCIN      string
	ClientContact  string
	ClientGSTIN    string
	Items          []InvoiceItemData
	TotalBeforeTax float64
	CGSTTotal      float64
	SGSTTotal      float64
	IGSTTotal      float64
	TaxTotal       float64
	RoundOff       string
	GrandTotal     float64
	AmountInWords  string
	BankName       string
	BankBranch     string
	AccountNumber  string
	IFSCCode       string
}

type InvoiceItemData struct {
	SrNo          int
	IsGroupHeader bool
	Description   string
	HSNSAC        string
	Qty           float64
	UnitPrice     float64
	TaxableValue  float64
	CGSTRate      float64
	CGSTAmount    float64
	SGSTRate      float64
	SGSTAmount    float64
	IGSTRate      float64
	IGSTAmount    float64
	TotalAmount   float64
}

type POData struct {
	PONumber        string
	PODate          string
	DeliveryDate    string
	PaymentTerms    string
	PlaceOfDelivery string
	PreparedBy      string
	VendorName      string
	ContactPerson   string
	VendorAddress   string
	VendorGSTIN     string
	VendorPhone     string
	VendorEmail     string
	Items           []POItemData
	Subtotal        float64
	TaxRate         float64
	TaxAmount       float64
	GrandTotal      float64
	Notes           []string
	ExecutionBy     string
	ExecutionTitle  string
	SignatoryName   string
	SignatoryRole   string
	SignatoryDIN    string
}

type POItemData struct {
	SrNo        int
	Description string
	Qty         float64
	UnitPrice   float64
	Amount      float64
}

type Generator struct{}

func NewGenerator() *Generator {
	return &Generator{}
}

const gstInvoiceHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  @page { size: A4 portrait; margin: 5mm; }
  body { font-family: 'Times New Roman', serif; font-size: 9.5pt; color: #020617; margin: 0; padding: 10px; line-height: 1.2; }
  @media print {
    @page { size: A4 portrait; margin: 5mm; }
    html, body { background: white !important; margin: 0 !important; padding: 0 !important; -webkit-print-color-adjust: exact !important; print-color-adjust: exact !important; }
    .no-print { display: none !important; }
  }
  .logo-banner { text-align: center; margin-bottom: 4px; }
  .logo-box { display: inline-block; background-color: #000; color: #fff; padding: 4px 20px; font-size: 11pt; font-weight: bold; letter-spacing: 2px; text-transform: uppercase; border-radius: 2px; }
  .outer-box { border: 1px solid #1e293b; }
  .title-bar { background-color: #eff6ff; border-bottom: 1px solid #1e293b; color: #1e3a8a; font-weight: bold; font-size: 10pt; text-align: center; padding: 4px; text-transform: uppercase; letter-spacing: 1px; }
  .header-grid { display: flex; justify-content: space-between; border-bottom: 1px solid #1e293b; padding: 8px; font-size: 9.5pt; }
  .issuer-info { width: 60%; }
  .invoice-info { width: 38%; text-align: right; font-weight: 600; }
  .bill-to { border-bottom: 1px solid #1e293b; padding: 8px; background-color: #f8fafc; font-size: 9.5pt; }
  table.items-table { width: 100%; border-collapse: collapse; border-bottom: 1px solid #1e293b; font-size: 9pt; }
  table.items-table th, table.items-table td { border: 1px solid #1e293b; padding: 4px; }
  table.items-table th { background-color: #f1f5f9; font-weight: 600; text-align: center; }
  .bottom-grid { display: flex; font-size: 9pt; }
  .left-col { width: 58%; border-right: 1px solid #1e293b; padding: 8px; display: flex; flex-direction: column; justify-content: space-between; }
  .right-col { width: 42%; display: flex; flex-direction: column; justify-content: space-between; }
  table.tax-summary { width: 100%; border-collapse: collapse; font-size: 9pt; }
  table.tax-summary td { border-bottom: 1px solid #cbd5e1; padding: 4px; }
  .mono { font-family: monospace; }
  .text-right { text-align: right; }
  .text-center { text-align: center; }
  .font-bold { font-weight: bold; }
  .signatory { text-align: center; padding: 8px; margin-top: 8px; }
</style>
</head>
<body>
  <div class="no-print" style="max-width: 210mm; margin: 0 auto 12px auto; display: flex; justify-content: space-between; align-items: center; padding: 10px 16px; background: #f8fafc; border: 1px solid #cbd5e1; border-radius: 6px; font-family: system-ui, -apple-system, sans-serif;">
    <button onclick="window.close()" style="background: #e2e8f0; color: #1e293b; border: none; padding: 8px 16px; border-radius: 4px; font-weight: 600; cursor: pointer; font-size: 13px;">
      ← Close Window
    </button>
    <button onclick="window.print()" style="background: #2563eb; color: #ffffff; border: none; padding: 8px 20px; border-radius: 4px; font-weight: 600; cursor: pointer; display: flex; align-items: center; gap: 8px; font-size: 14px; box-shadow: 0 2px 4px rgba(37,99,235,0.2);">
      🖨️ Print / Save as PDF
    </button>
  </div>

  <div class="logo-banner">
    <div class="logo-box">AUTONEX</div>
  </div>

  <div class="outer-box">
    <div class="title-bar">GST TAX INVOICE</div>

    <div class="header-grid">
      <div class="issuer-info">
        <p className="font-bold" style="font-weight:bold; margin:0 0 2px 0;">{{.IssuerName}}</p>
        <p style="margin:0 0 2px 0; color:#334155;">{{.IssuerAddress}}</p>
        <p style="margin:0 0 2px 0; color:#334155;">Contact - {{.IssuerContact}}</p>
        <p style="margin:0 0 4px 0; color:#1d4ed8; text-decoration:underline;">{{.IssuerEmail}}</p>
        <div style="font-weight:bold;">
          <span>GSTIN: {{.IssuerGSTIN}}</span> &nbsp;&nbsp;&nbsp;&nbsp; <span>PAN: {{.IssuerPAN}}</span>
        </div>
      </div>
      <div class="invoice-info">
        <p style="margin:0 0 4px 0;">Invoice No. : <span class="mono font-bold">{{.InvoiceNumber}}</span></p>
        <p style="margin:0;">Invoice Date : <span>{{.InvoiceDate}}</span></p>
      </div>
    </div>

    <div class="bill-to">
      <p style="margin:0 0 2px 0; font-size:8.5pt; color:#64748b; font-weight:bold; text-transform:uppercase;">BILL TO:</p>
      <p style="margin:0 0 2px 0; font-weight:bold;">{{.ClientName}}</p>
      <p style="margin:0 0 2px 0; color:#1e293b;">{{.ClientAddress}}{{if .ClientCIN}} CIN No: {{.ClientCIN}}{{end}}</p>
      <p style="margin:0 0 2px 0; color:#1e293b;">Contact: {{.ClientContact}}</p>
      <p style="margin:0; font-weight:bold;">GSTIN: {{.ClientGSTIN}}</p>
    </div>

    <table class="items-table">
      <thead>
        <tr>
          <th rowspan="2" style="width:4%;">Sr. No.</th>
          <th rowspan="2" style="width:32%; text-align:left;">Name of Product / Service</th>
          <th rowspan="2" style="width:11%;">HSN / SAC</th>
          <th rowspan="2" style="width:4%;">QTY</th>
          <th rowspan="2" style="width:8%; text-align:right;">Amount Per pc</th>
          <th rowspan="2" style="width:9%; text-align:right;">Taxable Value</th>
          <th colspan="2">CGST</th>
          <th colspan="2">SGST</th>
          <th colspan="2">IGST</th>
          <th rowspan="2" style="width:9%; text-align:right;">Total</th>
        </tr>
        <tr>
          <th style="width:4%;">Rate</th><th style="width:6%;">Amount</th>
          <th style="width:4%;">Rate</th><th style="width:6%;">Amount</th>
          <th style="width:4%;">Rate</th><th style="width:6%;">Amount</th>
        </tr>
      </thead>
      <tbody>
        {{range .Items}}
        {{if .IsGroupHeader}}
        <tr style="background-color:#f8fafc; font-weight:bold; font-style:italic;">
          <td class="text-center">{{.SrNo}}</td>
          <td colspan="11">{{.Description}}</td>
          <td></td>
        </tr>
        {{else}}
        <tr>
          <td class="text-center">{{.SrNo}}</td>
          <td>{{.Description}}</td>
          <td class="text-center mono">{{.HSNSAC}}</td>
          <td class="text-center font-bold">{{.Qty}}</td>
          <td class="text-right mono">{{printf "%.2f" .UnitPrice}}</td>
          <td class="text-right mono font-bold">{{printf "%.2f" .TaxableValue}}</td>
          <td class="text-center">{{.CGSTRate}}%</td>
          <td class="text-right mono">{{printf "%.2f" .CGSTAmount}}</td>
          <td class="text-center">{{.SGSTRate}}%</td>
          <td class="text-right mono">{{printf "%.2f" .SGSTAmount}}</td>
          <td class="text-center">{{.IGSTRate}}%</td>
          <td class="text-right mono">{{printf "%.2f" .IGSTAmount}}</td>
          <td class="text-right mono font-bold">{{printf "%.2f" .TotalAmount}}</td>
        </tr>
        {{end}}
        {{end}}
        <tr style="background-color:#f1f5f9; font-weight:bold;">
          <td colspan="5" class="text-right" style="text-transform:uppercase;">Total :</td>
          <td class="text-right mono">{{printf "%.2f" .TotalBeforeTax}}</td>
          <td></td><td class="text-right mono">{{printf "%.2f" .CGSTTotal}}</td>
          <td></td><td class="text-right mono">{{printf "%.2f" .SGSTTotal}}</td>
          <td></td><td class="text-right mono">{{printf "%.2f" .IGSTTotal}}</td>
          <td class="text-right mono font-bold" style="color:#1e3a8a;">{{printf "%.2f" .GrandTotal}}</td>
        </tr>
      </tbody>
    </table>

    <div class="bottom-grid">
      <div class="left-col">
        <div>
          <p style="margin:0 0 2px 0; font-weight:bold; color:#475569;">Total Invoice Amount in Words:</p>
          <p style="margin:0 0 8px 0; font-weight:bold; font-style:italic; background:#f8fafc; padding:6px; border:1px solid #e2e8f0;">{{.AmountInWords}}</p>
        </div>
        <div>
          <p style="margin:0 0 4px 0; font-weight:bold; color:#64748b; font-size:8.5pt; text-transform:uppercase;">BANK DETAILS :</p>
          <p style="margin:0 0 2px 0;">• Name of Bank : <strong>{{.BankName}}</strong></p>
          <p style="margin:0 0 2px 0;">• Bank Branch : <strong>{{.BankBranch}}</strong></p>
          <p style="margin:0 0 2px 0;">• Bank Account Number : <strong class="mono">{{.AccountNumber}}</strong></p>
          <p style="margin:0 0 6px 0;">• Bank Branch IFSC : <strong class="mono">{{.IFSCCode}}</strong></p>
        </div>
        <div style="border-top:1px solid #e2e8f0; padding-top:4px; font-size:8.5pt; color:#475569;">
          <p style="margin:0 0 2px 0; font-weight:bold; color:#000;">Terms and Conditions :</p>
          <p style="margin:0;">1. We declare that this invoice shows the actual price of the goods/services described and that all particulars are true and correct.</p>
        </div>
      </div>

      <div class="right-col">
        <table class="tax-summary">
          <tbody>
            <tr><td style="width:65%;">Total Amount Before Tax :</td><td class="text-right mono font-bold">{{printf "%.2f" .TotalBeforeTax}}</td></tr>
            <tr><td>Add : CGST :</td><td class="text-right mono">{{printf "%.2f" .CGSTTotal}}</td></tr>
            <tr><td>Add : SGST :</td><td class="text-right mono">{{printf "%.2f" .SGSTTotal}}</td></tr>
            <tr><td>Add : IGST :</td><td class="text-right mono">{{printf "%.2f" .IGSTTotal}}</td></tr>
            <tr style="background:#f8fafc; font-weight:bold;"><td>Tax Amount : GST :</td><td class="text-right mono">{{printf "%.2f" .TaxTotal}}</td></tr>
            <tr><td>Round off :</td><td class="text-right mono">{{.RoundOff}}</td></tr>
            <tr style="background:#eff6ff; font-weight:bold; color:#1e3a8a; font-size:10pt;"><td>Grand Total (Incl. GST) :</td><td class="text-right mono">{{printf "%.2f" .GrandTotal}}</td></tr>
            <tr><td style="font-size:8.5pt;">GST Payable on Reverse Charge :</td><td class="text-right font-bold" style="font-size:8.5pt;">No</td></tr>
          </tbody>
        </table>

        <div class="signatory">
          <p style="font-weight:bold; margin:0 0 2px 0;">For {{.IssuerName}}</p>
          <div style="margin:4px auto; text-align:center;">
            <img src="/autonex-seal.jpg" alt="Company Seal & Signature" style="max-height:56px; max-width:80px; object-fit:contain; display:inline-block;" />
          </div>
          <p style="font-weight:bold; border-top:1px solid #94a3b8; display:inline-block; padding-top:4px; margin:0;">Authorised Signatory</p>
        </div>
      </div>
    </div>
  </div>
</body>
</html>`

const poTemplateHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  @page { size: A4 portrait; margin: 5mm; }
  body { font-family: Arial, sans-serif; font-size: 10pt; color: #000; margin: 0; padding: 10px; line-height: 1.2; }
  @media print {
    @page { size: A4 portrait; margin: 5mm; }
    html, body { background: white !important; margin: 0 !important; padding: 0 !important; -webkit-print-color-adjust: exact !important; print-color-adjust: exact !important; }
    .no-print { display: none !important; }
  }
  .header { border-bottom: 1px solid #000; padding-bottom: 6px; margin-bottom: 8px; }
  .header-flex { display: flex; justify-content: space-between; align-items: center; }
  .company-title { font-weight: bold; font-size: 11pt; color: #111827; }
  .legal-info { font-size: 9.5pt; color: #374151; text-align: right; }
  .doc-title { text-align: center; font-size: 14pt; font-weight: bold; letter-spacing: 1px; margin: 10px 0; text-transform: uppercase; }
  table.info-grid { width: 100%; border-collapse: collapse; border: 1px solid #000; margin-bottom: 10px; font-size: 9.5pt; }
  table.info-grid td { border: 1px solid #000; padding: 4px 6px; }
  table.info-grid td.label { background-color: #f3f4f6; font-weight: 600; width: 16.6%; }
  table.info-grid td.val { width: 33.3%; }
  table.items-table { width: 100%; border-collapse: collapse; border: 1px solid #000; margin-bottom: 10px; font-size: 9.5pt; }
  table.items-table th, table.items-table td { border: 1px solid #000; padding: 5px; }
  table.items-table th { background-color: #e5e7eb; font-weight: bold; text-align: center; }
  .notes-box { border: 1px solid #d1d5db; background-color: #f9fafb; padding: 8px; border-radius: 4px; margin-bottom: 10px; font-size: 9pt; }
  .execution-flex { border-top: 1px solid #000; padding-top: 10px; display: flex; justify-content: space-between; margin-top: 12px; font-size: 9.5pt; }
  .mono { font-family: monospace; }
  .text-right { text-align: right; }
  .text-center { text-align: center; }
  .font-bold { font-weight: bold; }
</style>
</head>
<body>
  <div class="no-print" style="max-width: 210mm; margin: 0 auto 12px auto; display: flex; justify-content: space-between; align-items: center; padding: 10px 16px; background: #f8fafc; border: 1px solid #cbd5e1; border-radius: 6px; font-family: system-ui, -apple-system, sans-serif;">
    <button onclick="window.close()" style="background: #e2e8f0; color: #1e293b; border: none; padding: 8px 16px; border-radius: 4px; font-weight: 600; cursor: pointer; font-size: 13px;">
      ← Close Window
    </button>
    <button onclick="window.print()" style="background: #2563eb; color: #ffffff; border: none; padding: 8px 20px; border-radius: 4px; font-weight: 600; cursor: pointer; display: flex; align-items: center; gap: 8px; font-size: 14px; box-shadow: 0 2px 4px rgba(37,99,235,0.2);">
      🖨️ Print / Save as PDF
    </button>
  </div>
  <div class="header">
    <div class="header-flex">
      <div>
        <img src="/autonex-po-logo.png" alt="AUTONEX AI" style="height: 40px; width: auto; max-width: 176px; object-fit: contain;" />
      </div>
      <div class="legal-info">
        <p class="company-title" style="margin:0;">AUTONEX AI 360 PRIVATE LIMITED</p>
        <p style="margin:0;">CIN: U62099MH2025PTC453218 | GSTIN: 27ABDCA3903H1ZX</p>
      </div>
    </div>
    <p style="text-align:center; font-size:9pt; color:#4b5563; margin:4px 0 0 0; border-top:1px solid #e5e7eb; padding-top:2px;">
      908, Lodha Supremus, Saki Vihar Road, Powai - 400072, Maharashtra, India
    </p>
  </div>

  <div class="doc-title">PURCHASE ORDER (PO)</div>

  <table class="info-grid">
    <tbody>
      <tr>
        <td class="label">Vendor Name</td><td class="val">{{.VendorName}}</td>
        <td class="label">PO No.</td><td class="val mono font-bold">{{.PONumber}}</td>
      </tr>
      <tr>
        <td class="label">Contact Person</td><td class="val">{{.ContactPerson}}</td>
        <td class="label">PO Date</td><td class="val">{{.PODate}}</td>
      </tr>
      <tr>
        <td class="label">Vendor Address</td><td class="val">{{.VendorAddress}}</td>
        <td class="label">Delivery Date</td><td class="val">{{.DeliveryDate}}</td>
      </tr>
      <tr>
        <td class="label">Vendor GSTIN</td><td class="val mono">{{.VendorGSTIN}}</td>
        <td class="label">Payment Terms</td><td class="val">{{.PaymentTerms}}</td>
      </tr>
      <tr>
        <td class="label">Contact No.</td><td class="val">{{.VendorPhone}}</td>
        <td class="label">Place of Delivery</td><td class="val">{{.PlaceOfDelivery}}</td>
      </tr>
      <tr>
        <td class="label">Email ID</td><td class="val">{{.VendorEmail}}</td>
        <td class="label">Prepared By</td><td class="val">{{.PreparedBy}}</td>
      </tr>
    </tbody>
  </table>

  <table class="items-table">
    <thead>
      <tr>
        <th style="width:8%;">Sr. No.</th>
        <th style="width:47%; text-align:left;">Description</th>
        <th style="width:10%;">Qty</th>
        <th style="width:17.5%; text-align:right;">Unit Price (₹)</th>
        <th style="width:17.5%; text-align:right;">Amount (₹)</th>
      </tr>
    </thead>
    <tbody>
      {{range .Items}}
      <tr>
        <td class="text-center font-bold">{{.SrNo}}</td>
        <td>{{.Description}}</td>
        <td class="text-center font-bold">{{.Qty}}</td>
        <td class="text-right mono">{{printf "%.2f" .UnitPrice}}</td>
        <td class="text-right mono">{{printf "%.2f" .Amount}}</td>
      </tr>
      {{end}}
      <tr style="background:#f9fafb;">
        <td colspan="4" class="text-right font-bold">Sub Total</td>
        <td class="text-right mono font-bold">{{printf "%.2f" .Subtotal}}</td>
      </tr>
      <tr style="background:#f9fafb;">
        <td colspan="4" class="text-right font-bold">GST ({{.TaxRate}}%)</td>
        <td class="text-right mono font-bold">{{printf "%.2f" .TaxAmount}}</td>
      </tr>
      <tr style="background:#f3f4f6; font-size:11pt; font-weight:bold;">
        <td colspan="4" class="text-right">Grand Total</td>
        <td class="text-right mono">{{printf "%.2f" .GrandTotal}}</td>
      </tr>
    </tbody>
  </table>

  {{if .Notes}}
  <div class="notes-box">
    <strong style="display:block; margin-bottom:4px;">Notes & Scope Details:</strong>
    <ul style="margin:0; padding-left:18px;">
      {{range .Notes}}
      <li>{{.}}</li>
      {{end}}
    </ul>
  </div>
  {{end}}

  <div class="execution-flex">
    <div style="width:48%;">
      <p class="font-bold" style="margin:0 0 4px 0;">Execution / Acceptance:</p>
      <p style="margin:0 0 2px 0;">By: <u>{{if .ExecutionBy}}{{.ExecutionBy}}{{else}}Nikhil Gawade{{end}}</u></p>
      <p style="margin:0 0 2px 0;">Title: <u>{{if .ExecutionTitle}}{{.ExecutionTitle}}{{else}}Founder & CEO{{end}}</u></p>
      <p style="font-size:8.5pt; color:#6b7280; margin:4px 0 0 0;">Autonex AI 360 Private Limited</p>
    </div>
    <div style="width:48%; text-align:right;">
      <p class="font-bold" style="text-transform:uppercase; margin:0 0 2px 0;">Authorized Signatory</p>
      <p class="font-bold" style="margin:0 0 4px 0;">AUTONEX AI 360 PRIVATE LIMITED</p>
      <div style="display:flex; justify-content:flex-end; margin:4px 0;">
        <div style="border:1px dashed #cbd5e1; border-radius:4px; padding:2px; display:inline-block;">
          <img src="/autonex-seal.jpg" alt="Company Seal" style="max-height:56px; max-width:80px; object-fit:contain;" />
        </div>
      </div>
      <p class="font-bold" style="margin:0;">{{if .SignatoryName}}{{.SignatoryName}}{{else}}NIKHIL SUNIL GAWADE{{end}}</p>
      <p style="color:#4b5563; margin:0;">{{if .SignatoryRole}}{{.SignatoryRole}}{{else}}Director{{end}}</p>
      <p style="font-size:8.5pt; color:#6b7280; margin:0;">DIN: {{if .SignatoryDIN}}{{.SignatoryDIN}}{{else}}11217265{{end}}</p>
    </div>
  </div>
</body>
</html>`

func (g *Generator) GenerateInvoiceHTML(ctx context.Context, data InvoiceData) (io.Reader, error) {
	tmpl, err := template.New("invoice").Parse(gstInvoiceHTML)
	if err != nil {
		return nil, fmt.Errorf("invoice template error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("invoice render error: %w", err)
	}

	return &buf, nil
}

func (g *Generator) GeneratePOHTML(ctx context.Context, data POData) (io.Reader, error) {
	tmpl, err := template.New("po").Parse(poTemplateHTML)
	if err != nil {
		return nil, fmt.Errorf("po template error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("po render error: %w", err)
	}

	return &buf, nil
}

// ConvertNumberToWords converting integer amounts to Indian currency words
func ConvertNumberToWords(amount float64) string {
	val := int64(amount)
	if val == 0 {
		return "Rupees Zero Only"
	}
	return fmt.Sprintf("Rupees %s Only", strings.Title(convertUnderThousand(val)))
}

func convertUnderThousand(n int64) string {
	return fmt.Sprintf("%d", n)
}
