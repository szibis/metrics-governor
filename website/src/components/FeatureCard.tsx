import React from 'react';

interface FeatureCardProps {
  title: string;
  description: string;
  icon: string;
  link: string;
}

export default function FeatureCard({title, description, icon, link}: FeatureCardProps) {
  return (
    <a href={link} className="feature-card">
      <div className="feature-card__icon">{icon}</div>
      <h3>{title}</h3>
      <p>{description}</p>
    </a>
  );
}
