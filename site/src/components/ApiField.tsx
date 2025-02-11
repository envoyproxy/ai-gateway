import React from 'react';
import clsx from 'clsx';

interface ApiFieldProps {
  name: string;
  type: string;
  required: string;
  description?: string;
  enumValues?: string[];
  defaultValue?: string;
}

// Helper function to convert code content to HTML while preserving existing HTML
const processDescription = (description: string): string => {
  // First handle triple backtick code blocks with optional language
  let processed = description.replace(/```(\w+)?\s*([\s\S]*?)```/g, (match, lang, codeContent) => {
    const language = lang || '';
    const languageClass = language ? ` language-${language}` : '';
    return `<pre class="prism-code${languageClass}"><code class="codeBlock${languageClass}">${codeContent.trim()}</code></pre>`;
  });

  // Then handle single backtick inline code
  processed = processed.replace(/`([^`]+)`/g, '<code class="thin">$1</code>');

  return processed;
};

const ApiField: React.FC<ApiFieldProps> = ({
  name,
  type,
  required,
  description,
  enumValues,
  defaultValue
}) => {
  const isEnum = type === 'enum';

  return (
    <div className="api-field" data-type={isEnum ? 'enum' : undefined}>
      {isEnum ? (
        <>
          <div className="api-field__left">
            <code className="api-field__name">{name}</code>
          </div>
          <div className="api-field__right">
            {description && (
              <div className="api-field__description" dangerouslySetInnerHTML={{ __html: processDescription(description) }}/>
            )}
          </div>
        </>
      ) : (
        <>
          <div className="api-field__left">
            <code className="api-field__name">{name}</code>
            <span className={clsx(
              'api-field__badge',
              required === "true" ? 'api-field__badge--required' : 'api-field__badge--optional'
            )}>
              {required === "true" ? 'required' : 'optional'}
            </span>
            <div className="api-field__type">
              <div dangerouslySetInnerHTML={{
                __html: processDescription(type).replace(
                  /\[([^\]]+)\]\(([^)]+)\)/g,
                  '<a href="$2">$1</a>'
                )
              }}/>
            </div>
          </div>
          <div className="api-field__right">
            {defaultValue && (
              <div className="api-field__default">
                {defaultValue}
              </div>
            )}
            {description && (
              <div className="api-field__description" dangerouslySetInnerHTML={{ __html: processDescription(description) }}/>
            )}
            {enumValues && (
              <div className="api-field__enum">
                <strong>Enum:</strong>{' '}
                {enumValues.map(v => <code key={v}>{v}</code>)}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
};

export default ApiField;
