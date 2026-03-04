# Adopter Logos

This directory contains logos of organizations that use Envoy AI Gateway in production.

## Adding Your Organization's Logo

We'd love to feature your organization! The easiest way is to use an external logo URL - no need to upload files!

### Quick Add (Recommended)

**[Edit adopters.json on GitHub →](https://github.com/envoyproxy/ai-gateway/edit/main/site/src/data/adopters/adopters.json)**

Simply add your organization with a direct link to your logo:

```json
{
  "name": "Your Company",
  "logoUrl": "https://yoursite.com/logo.svg",
  "url": "https://yourcompany.com",
  "description": "Optional description shown on hover"
}
```

### Logo Options

#### Option 1: External URL (Easiest - No Upload Required!)

Use a direct link to your logo hosted on your website or CDN:

```json
"logoUrl": "https://yourcompany.com/assets/logo.svg"
```

**Benefits:**

- No file upload needed
- You control the logo and can update it anytime
- Works immediately

#### Option 2: Local Logo (Better Performance)

If you prefer to host the logo in this repository:

1. **Prepare Your Logo**
   - **Format**: SVG preferred (PNG also acceptable)
   - **Size**: Optimal dimensions are 240x160px or similar 3:2 ratio
   - **Background**: Transparent or white background works best
   - **File naming**: Use lowercase with hyphens (e.g., `my-company.svg`)

2. **Submit via Pull Request**
   - Fork the [ai-gateway repository](https://github.com/envoyproxy/ai-gateway)
   - Add your logo file to `site/static/img/adopters/`
   - Update `site/src/data/adopters/adopters.json` with your entry:
     ```json
     {
       "name": "Your Company",
       "logoUrl": "/img/adopters/your-company.svg",
       "url": "https://yourcompany.com"
     }
     ```
   - Submit a pull request with the title "site: Add [Company Name] to adopters"

### Guidelines

- Logos should represent organizations actively using Envoy AI Gateway in production
- Please ensure you have permission to use the logo
- Logos should be family-friendly and professional
- We reserve the right to remove logos that don't meet our community standards

### Questions?

If you have questions about adding your logo, please:

- Ask in [GitHub Discussions](https://github.com/envoyproxy/ai-gateway/discussions)
- Join our [Slack community](https://communityinviter.com/apps/envoyproxy/envoy)

Thank you for supporting Envoy AI Gateway!
