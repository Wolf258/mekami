import React from 'react';
import Link from '@docusaurus/Link';
import clsx from 'clsx';
import styles from './CardGrid.module.css';

export default function CardGrid({ children, className }) {
  return <div className={clsx(styles.grid, className)}>{children}</div>;
}

export function Card({ icon, title, description, to, href }) {
  const isInternal = !href;
  const linkProps = isInternal ? { to } : { href };
  const LinkComponent = isInternal ? Link : 'a';
  return (
    <LinkComponent className={styles.card} {...linkProps}>
      {icon && <div className={styles.icon}>{icon}</div>}
      <h3 className={styles.title}>{title}</h3>
      {description && <p className={styles.description}>{description}</p>}
    </LinkComponent>
  );
}
