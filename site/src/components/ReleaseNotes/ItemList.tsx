import React from 'react';

interface ListItem {
  title: string;
  description: string;
  icon?: string;
}

interface ItemListProps {
  items: ListItem[];
  className?: string;
}

export const ItemList: React.FC<ItemListProps> = ({ items, className = '' }) => {
  return (
    <ul className={className}>
      {items.map((item, index) => (
        <li key={index} style={{ marginBottom: '0.5rem' }}>
          {item.icon && <span style={{ marginRight: '0.5rem' }}>{item.icon}</span>}
          <b dangerouslySetInnerHTML={{ __html: item.title }}></b>
          {': '}
          <span dangerouslySetInnerHTML={{ __html: item.description }}></span>
        </li>
      ))}
    </ul>
  );
};
